FROM alpine:3.19

ENV TZ=Asia/Shanghai
RUN apk add --no-cache tzdata ca-certificates

WORKDIR /app
COPY tusd .

EXPOSE 1080


# 只使用 CMD，所有参数都可以在运行时覆盖
CMD ["./tusd", "--port", "1080"]


