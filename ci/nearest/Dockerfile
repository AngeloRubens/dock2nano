FROM python:3.12-alpine
EXPOSE 8000
# the shell stays (it waits), with a sleeping loop in a subshell and the server as its children
CMD ["sh", "-c", "(while :; do sleep 60; done) & python3 -m http.server 8000 --bind 0.0.0.0 & wait"]
