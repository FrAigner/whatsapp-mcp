# MCP server (Python). Reads the bridge's store read-only (mounted at /app/store).
FROM python:3.12-slim-bookworm
RUN useradd --uid 10001 --create-home --home-dir /app app
WORKDIR /srv
COPY whatsapp-mcp-server/ /srv/
RUN pip install --no-cache-dir .
USER app
WORKDIR /app
ENV PYTHONPATH=/srv
CMD ["python", "/srv/main.py"]
