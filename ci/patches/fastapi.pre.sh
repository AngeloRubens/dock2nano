# gunicorn forks its workers and nanos has no fork: the observation has to run uvicorn in one
# process, so the command goes into the compose file before the conversion.
f=$(ls /tmp/ac/fastapi/compose.yaml /tmp/ac/fastapi/compose.yml /tmp/ac/fastapi/docker-compose.yml 2>/dev/null | head -1)
sed -i 's#^\(\s*\)container_name: fastapi-application#&\n\1command: uvicorn app.main:app --host 0.0.0.0 --port 8000#' "$f"
cat "$f"
grep -q 'command: uvicorn' "$f"
