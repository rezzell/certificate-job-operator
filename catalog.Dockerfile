FROM quay.io/operator-framework/opm:v1.55.0
COPY catalog /configs
EXPOSE 50051
USER 65532:65532
ENTRYPOINT ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache"]
