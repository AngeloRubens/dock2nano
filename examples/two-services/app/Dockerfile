FROM alpine:3.20
RUN apk add --no-cache busybox-extras && mkdir /www && echo "app ok" > /www/index.html
EXPOSE 8081
CMD ["httpd", "-f", "-p", "8081", "-h", "/www"]
