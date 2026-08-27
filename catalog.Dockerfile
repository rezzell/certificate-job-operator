FROM quay.io/operator-framework/opm@sha256:e5a6220603fb4504d58c6e3e488386b817e3695c906a62ee0370b5faedc3799a
COPY catalog /configs
EXPOSE 50051
USER 65532:65532
ENTRYPOINT ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache"]
