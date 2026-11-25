FROM python:3.12-alpine
EXPOSE 8000
CMD ["sh", "-c", "(while :; do sleep 60; done) & python3 -m http.server 8000 --bind 0.0.0.0"]
