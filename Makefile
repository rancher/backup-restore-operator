# ---- CI Image Config ----
CI_IMAGE := ghcr.io/rancher/ci-image/go1.26
WORKDIR := /workspace

# Detect CI environment (common env var used by many CI systems)
CI ?= false

# Docker run wrapper (only used locally)
DOCKER_RUN = docker run --rm -i \
	-v $(PWD):$(WORKDIR) \
	-w $(WORKDIR) \
	$(CI_IMAGE)

# Command runner:
# - In CI: run commands directly
# - Locally: run via Docker
ifeq ($(CI),true)
	RUN =
else
	RUN = $(DOCKER_RUN)
endif

.PHONY: ci
ci:
	$(RUN) ./scripts/ci
