FROM alpine:3.24.0

COPY a.txt .
RUN cat a.txt
ENTRYPOINT ["cat", "a.txt"]
