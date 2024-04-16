FROM docker.io/golang:1.22

WORKDIR /usr/src/app

# pre-copy/cache go.mod for pre-downloading dependencies and only redownloading them in subsequent builds if they change
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .
RUN CGO_ENABLED=0 go build -ldflags '-extldflags "-static"' -v -o /usr/local/bin/kube-mod-cmp ./...
ENTRYPOINT ["/usr/src/app/entrypoint.sh"]
