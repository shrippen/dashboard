VENV ?= .venv
PY := $(VENV)/bin/python

.PHONY: check lint test run migrate

check: lint test

lint:
	$(VENV)/bin/ruff check app tests
	$(PY) -m app.tools.stylecheck

test:
	$(VENV)/bin/pytest

run:
	DASHBOARD_DEV=1 DATA_DIR=./data $(VENV)/bin/uvicorn app.main:create_app --factory --reload --port 8080

migrate:
	DATA_DIR=./data $(VENV)/bin/alembic revision --autogenerate -m "$(m)"
