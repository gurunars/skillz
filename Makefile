IMAGE := localhost/skillz-tools
RUN   := podman run --rm -v "$(CURDIR)":/work:rw -e GH_TOKEN $(IMAGE)

.PHONY: image check build validate publish clean

image:
	podman build -t $(IMAGE) -f Containerfile .

check: image
	$(RUN) check

build: image
	$(RUN) build

validate: image
	$(RUN) validate

# make publish TAG=v1.0.0   (requires GH_TOKEN in the environment)
publish: image
	$(RUN) publish $(TAG)

clean: image
	$(RUN) clean
