# syntax=docker/dockerfile:1

FROM python:3.12-slim
ENV PYTHONUNBUFFERED=1
WORKDIR /app

# Dependencies first, so this layer is cached until requirements.txt changes.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY worker.py .

RUN useradd --system --no-create-home app
USER app

ENV JOBQUEUE_URL=http://server:8080
CMD ["python", "worker.py"]