FROM alpine:latest

ADD lxcfs-webhook /lxcfs-webhook
ENTRYPOINT ["./lxcfs-webhook"]