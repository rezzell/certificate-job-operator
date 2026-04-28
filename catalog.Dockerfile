FROM quay.io/operator-framework/opm:latest
COPY catalog /configs
EXPOSE 50051
ENTRYPOINT ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache"]
