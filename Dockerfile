FROM golang:alpine AS builder

RUN apk update && apk add --no-cache git

ENV USER=appuser
# See https://stackoverflow.com/a/55757473/12429735RUN 
ENV UID=10001 

RUN adduser \    
    --disabled-password \    
    --gecos "" \    
    --home "/nonexistent" \    
    --shell "/sbin/nologin" \    
    --no-create-home \    
    --uid "${UID}" \    
    "${USER}"
    
WORKDIR $GOPATH/src/mypackage/myapp/

COPY ./spotify-playlist-maker .

RUN go get -d -v

RUN GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /go/bin/spotify-playlist-maker


FROM golang:alpine

EXPOSE 8080

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
COPY --from=builder /go/bin/spotify-playlist-maker /go/bin/spotify-playlist-maker

USER appuser:appuser

ENTRYPOINT ["/go/bin/spotify-playlist-maker"]