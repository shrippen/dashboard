# syntax=docker/dockerfile:1

# ── Build: wheel with all dependencies ──
FROM python:3.12-slim AS build
WORKDIR /src
COPY pyproject.toml ./
COPY app ./app
RUN pip wheel --no-cache-dir --wheel-dir /wheels .

# ── Runtime ──
FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DATA_DIR=/data \
    FORWARDED_ALLOW_IPS=127.0.0.1
RUN useradd --system --uid 10001 --home /app dashboard \
 && mkdir -p /data && chown dashboard /data
WORKDIR /app
COPY --from=build /wheels /wheels
RUN pip install --no-cache-dir /wheels/*.whl && rm -rf /wheels
USER dashboard
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8080/healthz', timeout=4).status == 200 else 1)"
CMD ["sh", "-c", "exec uvicorn app.main:create_app --factory --host 0.0.0.0 --port 8080 --proxy-headers --forwarded-allow-ips \"$FORWARDED_ALLOW_IPS\""]
