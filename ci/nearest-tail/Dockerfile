FROM python:3.12-alpine
EXPOSE 8000
# the last command of -c is a tail: the shell runs it in its own process (no clone)
CMD ["sh", "-c", "python3 -m http.server 8000 --bind 0.0.0.0 & tail -f /dev/null"]
