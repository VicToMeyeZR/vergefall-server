# Multi-stage plugin bake. Version pairing is STRICT:
#   pluginbuilder 3.21.1 (Go 1.21.6) / nakama 3.21.1 / nakama-common v1.31.0
FROM heroiclabs/nakama-pluginbuilder:3.21.1 AS builder
ENV GO111MODULE=on
ENV CGO_ENABLED=1
WORKDIR /backend
COPY go.mod go.sum ./
COPY sim ./sim
COPY nakama ./nakama
RUN go build -buildmode=plugin -trimpath -tags nakama -o ./vergefall.so ./nakama

FROM heroiclabs/nakama:3.21.1
COPY --from=builder /backend/vergefall.so /nakama/data/modules/vergefall.so
COPY local.yml /nakama/data/local.yml
COPY docker/start.sh /nakama/start.sh
USER root
RUN chmod +x /nakama/start.sh
EXPOSE 7349 7350 7351
ENTRYPOINT ["/nakama/start.sh"]
