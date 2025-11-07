FROM python:3.11-slim

ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY python/flag_submitter/requirements.txt requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

COPY python/flag_submitter /app

CMD ["python", "app.py"]
