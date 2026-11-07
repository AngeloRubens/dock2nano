FROM python:3.12-alpine
RUN mkdir /www && echo "app ok" > /www/index.html
EXPOSE 8081
CMD ["python", "-m", "http.server", "8081", "-d", "/www"]
