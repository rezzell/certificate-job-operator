package jobsecurity

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

var reservedOperatorLabels = map[string]struct{}{
	"app.kubernetes.io/managed-by":                   {},
	"certificates.rezzell.com/certificatejob":        {},
	"certificates.rezzell.com/certificate":           {},
	"certificates.rezzell.com/certificate-namespace": {},
	"certificates.rezzell.com/workflow-node":         {},
	"certificates.rezzell.com/run-id":                {},
}

func ValidateJobTemplateSecurity(spec *batchv1.JobSpec) error {
	podSpec := spec.Template.Spec

	if podSpec.HostNetwork {
		return fmt.Errorf("hostNetwork is not allowed")
	}
	if podSpec.HostPID {
		return fmt.Errorf("hostPID is not allowed")
	}
	if podSpec.HostIPC {
		return fmt.Errorf("hostIPC is not allowed")
	}
	if podSpec.ServiceAccountName != "" {
		return fmt.Errorf("serviceAccountName override is not allowed")
	}
	if BoolPointerTrue(podSpec.AutomountServiceAccountToken) {
		return fmt.Errorf("automountServiceAccountToken=true is not allowed")
	}
	if podSpec.SecurityContext != nil {
		if podSpec.SecurityContext.RunAsUser != nil && *podSpec.SecurityContext.RunAsUser == 0 {
			return fmt.Errorf("pod cannot run as root")
		}
		if podSpec.SecurityContext.RunAsNonRoot != nil && !*podSpec.SecurityContext.RunAsNonRoot {
			return fmt.Errorf("pod runAsNonRoot=false is not allowed")
		}
	}

	for _, volume := range podSpec.Volumes {
		if volume.HostPath != nil {
			return fmt.Errorf("hostPath volume %q is not allowed", volume.Name)
		}
		if volume.Projected != nil {
			for _, source := range volume.Projected.Sources {
				if source.ServiceAccountToken != nil {
					return fmt.Errorf("projected serviceAccountToken volume %q is not allowed", volume.Name)
				}
			}
		}
	}

	containers := make([]corev1.Container, 0, len(podSpec.InitContainers)+len(podSpec.Containers))
	containers = append(containers, podSpec.InitContainers...)
	containers = append(containers, podSpec.Containers...)
	for _, container := range containers {
		if container.SecurityContext == nil {
			continue
		}
		if BoolPointerTrue(container.SecurityContext.Privileged) {
			return fmt.Errorf("container %q cannot run privileged", container.Name)
		}
		if BoolPointerTrue(container.SecurityContext.AllowPrivilegeEscalation) {
			return fmt.Errorf("container %q cannot allow privilege escalation", container.Name)
		}
		if container.SecurityContext.Capabilities != nil && len(container.SecurityContext.Capabilities.Add) > 0 {
			return fmt.Errorf("container %q cannot add linux capabilities", container.Name)
		}
		if container.SecurityContext.RunAsUser != nil && *container.SecurityContext.RunAsUser == 0 {
			return fmt.Errorf("container %q cannot run as root", container.Name)
		}
		if container.SecurityContext.RunAsNonRoot != nil && !*container.SecurityContext.RunAsNonRoot {
			return fmt.Errorf("container %q runAsNonRoot=false is not allowed", container.Name)
		}
	}

	return nil
}

func ApplyJobSecurityDefaults(spec *batchv1.JobSpec, defaultTTL int32) {
	if spec.TTLSecondsAfterFinished == nil {
		ttl := defaultTTL
		spec.TTLSecondsAfterFinished = &ttl
	}

	podSpec := &spec.Template.Spec
	disabled := false
	podSpec.AutomountServiceAccountToken = &disabled
	if podSpec.EnableServiceLinks == nil {
		disabled := false
		podSpec.EnableServiceLinks = &disabled
	}

	if podSpec.SecurityContext == nil {
		podSpec.SecurityContext = &corev1.PodSecurityContext{}
	}
	if podSpec.SecurityContext.SeccompProfile == nil {
		podSpec.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}
	if podSpec.SecurityContext.RunAsNonRoot == nil {
		enabled := true
		podSpec.SecurityContext.RunAsNonRoot = &enabled
	}

	for i := range podSpec.InitContainers {
		applyContainerSecurityDefaults(&podSpec.InitContainers[i])
	}
	for i := range podSpec.Containers {
		applyContainerSecurityDefaults(&podSpec.Containers[i])
	}
}

func ContainsCapability(capabilities []corev1.Capability) bool {
	for _, capability := range capabilities {
		if capability == "ALL" {
			return true
		}
	}
	return false
}

func BoolPointerTrue(v *bool) bool {
	return v != nil && *v
}

func ValidateReservedLabels(labels map[string]string) error {
	for key := range labels {
		if _, reserved := reservedOperatorLabels[key]; reserved {
			return fmt.Errorf("label %q is reserved by the operator", key)
		}
	}
	return nil
}

func applyContainerSecurityDefaults(container *corev1.Container) {
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	if container.SecurityContext.AllowPrivilegeEscalation == nil {
		disabled := false
		container.SecurityContext.AllowPrivilegeEscalation = &disabled
	}
	if container.SecurityContext.SeccompProfile == nil {
		container.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}
	if container.SecurityContext.RunAsNonRoot == nil {
		enabled := true
		container.SecurityContext.RunAsNonRoot = &enabled
	}
	if container.SecurityContext.Capabilities == nil {
		container.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
		return
	}
	if !ContainsCapability(container.SecurityContext.Capabilities.Drop) {
		container.SecurityContext.Capabilities.Drop = append(container.SecurityContext.Capabilities.Drop, "ALL")
	}
}
