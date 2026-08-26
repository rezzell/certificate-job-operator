FROM quay.io/operator-framework/opm@sha256:a061e0d7d818d1e4f624d8ae160319e65c03b7788b16211faf0e8bf186b5a70e
COPY catalog /configs
EXPOSE 50051
USER 65532:65532
ENTRYPOINT ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache"]
