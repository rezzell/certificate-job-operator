//go:build integration
// +build integration

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	certmgrv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	certificatesv1alpha1 "github.com/russell/certificate-job-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/russell/certificate-job-operator/test/utils"
)

const cmctlVersion = "v2.5.0"

func manifestForRenewalTest(ns, issuerName, cjobName, certName, secretName string) string {
	manifest := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: %s
  namespace: %s
spec:
  selfSigned: {}
---
apiVersion: certificates.rezzell.com/v1alpha1
kind: CertificateJob
metadata:
  name: %s
  namespace: %s
spec:
  certificateSelector:
    matchLabels:
      renewal-test: "true"
  parallelism: 1
  failurePolicy: StopDownstream
  jobs:
  - name: verify-secret
    template:
      template:
        spec:
          restartPolicy: Never
          containers:
          - name: verify
            image: %s
            securityContext:
              allowPrivilegeEscalation: false
              runAsNonRoot: true
              runAsUser: 1000
              capabilities:
                drop:
                - ALL
            command:
            - /bin/sh
            - -ec
            - |
              test -s /var/run/certificate-input/tls.crt
              test -s /var/run/certificate-input/tls.key
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %s
  namespace: %s
  labels:
    renewal-test: "true"
spec:
  secretName: %s
  issuerRef:
    kind: Issuer
    name: %s
  duration: 1h
  renewBefore: 50m
  commonName: renewal-test.local
  dnsNames:
  - renewal-test.local
  - renewal-test.internal
	`, issuerName, ns, cjobName, ns, curlImage, certName, ns, secretName, issuerName)

	return strings.TrimSpace(manifest) + "\n"
}

func applyTempManifest(manifest string) {
	path := writeTempManifest(manifest)
	var err error
	for attempt := 1; attempt <= 30; attempt++ {
		cmd := exec.Command("kubectl", "apply", "-f", path)
		_, err = utils.Run(cmd)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "failed calling webhook") {
			break
		}
		time.Sleep(2 * time.Second)
	}
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		deleteResource("-f", path)
		_ = os.Remove(path)
	})
}

func writeTempManifest(manifest string) string {
	dir := filepath.Join(os.TempDir(), "certificate-job-operator-e2e")
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())

	file, err := os.CreateTemp(dir, "renewal-*.yaml")
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())
	Expect(os.WriteFile(file.Name(), []byte(manifest), 0o644)).To(Succeed())
	return file.Name()
}

func deleteResource(kind, name string) {
	args := []string{"delete"}
	if kind == "-f" {
		args = append(args, kind, name, "--ignore-not-found=true")
	} else {
		args = append(args, kind, name, "-n", namespace, "--ignore-not-found=true")
	}
	cmd := exec.Command("kubectl", args...)
	_, _ = utils.Run(cmd)
}

func getCertificateJob(name string) certificatesv1alpha1.CertificateJob {
	var cjob certificatesv1alpha1.CertificateJob
	getJSONResource("certificatejobs", name, &cjob)
	return cjob
}

func getCertificate(name string) certmgrv1.Certificate {
	var cert certmgrv1.Certificate
	getJSONResource("certificates", name, &cert)
	return cert
}

func getSecret(name string) corev1.Secret {
	var secret corev1.Secret
	getJSONResource("secrets", name, &secret)
	return secret
}

func getJSONResource(kind, name string, target any) {
	cmd := exec.Command("kubectl", "get", kind, name, "-n", namespace, "-o", "json")
	output, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	Expect(json.Unmarshal([]byte(output), target)).To(Succeed())
}

func isCertificateReady(cert certmgrv1.Certificate) bool {
	for _, condition := range cert.Status.Conditions {
		if condition.Type == certmgrv1.CertificateConditionReady && condition.Status == cmmeta.ConditionTrue {
			return true
		}
	}
	return false
}

func certificateRevision(cert certmgrv1.Certificate) int64 {
	if cert.Status.Revision == nil {
		return 0
	}
	return int64(*cert.Status.Revision)
}

func renewCertificate(name string) {
	cert := getCertificate(name)

	cmctl := ensureCmctlBinary()
	cmd := exec.Command(cmctl, "renew", name, "-n", namespace)
	_, err := utils.Run(cmd)
	if err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "cmctl renew failed, falling back to secret deletion: %v\n", err)
		if cert.Spec.SecretName != "" {
			deleteCmd := exec.Command("kubectl", "delete", "secret", cert.Spec.SecretName, "-n", namespace, "--ignore-not-found=true")
			_, err = utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

func ensureCmctlBinary() string {
	binDir := filepath.Join(os.TempDir(), "certificate-job-operator-tools")
	cmctl := filepath.Join(binDir, "cmctl")
	if _, err := os.Stat(cmctl); err == nil {
		return cmctl
	}

	Expect(os.MkdirAll(binDir, 0o755)).To(Succeed())
	cmd := exec.Command("go", "install", "github.com/cert-manager/cmctl/v2@"+cmctlVersion)
	cmd.Env = append(os.Environ(), "GOBIN="+binDir)
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	return cmctl
}

func listJobsByLabel(selector string) []batchv1.Job {
	cmd := exec.Command("kubectl", "get", "jobs", "-n", namespace, "-l", selector, "-o", "json")
	output, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	var list batchv1.JobList
	Expect(json.Unmarshal([]byte(output), &list)).To(Succeed())
	return list.Items
}

func countSucceededJobs(jobs []batchv1.Job) int {
	count := 0
	for i := range jobs {
		if jobs[i].Status.Succeeded > 0 {
			count++
		}
	}
	return count
}
