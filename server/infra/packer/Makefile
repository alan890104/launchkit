.PHONY: init validate image clean

# Default vars (override via env or make image PROJECT_ID=xxx)
PROJECT_ID ?= $(shell gcloud config get-value project 2>/dev/null)
ZONE       ?= us-east4-c
REGION     ?= us-east4

init:
	packer init buildkit.pkr.hcl

validate: init
	packer validate \
		-var "project_id=$(PROJECT_ID)" \
		-var "zone=$(ZONE)" \
		-var "artifact_registry_region=$(REGION)" \
		buildkit.pkr.hcl

image: init
	packer build \
		-var "project_id=$(PROJECT_ID)" \
		-var "zone=$(ZONE)" \
		-var "artifact_registry_region=$(REGION)" \
		buildkit.pkr.hcl

clean:
	@echo "Listing images in launchkit-buildkit family (delete old ones manually):"
	gcloud compute images list --filter="family=launchkit-buildkit" --format="table(name,creationTimestamp,status)"
