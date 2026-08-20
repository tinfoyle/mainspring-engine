FROM alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659

# The launcher integration gate needs a bounded container that stays alive
# long enough to verify identity and cancellation. The production launcher
# never permits this mutable local fixture outside local-secure verification.
ENTRYPOINT ["/bin/sh", "-c", "sleep 300"]
