#!/usr/bin/env bash
#
# This script builds skuld for Intel and multiple Arm processors, using
# docker buildx cross compilers. To build the latest skuld release, do:
#
# 1. Update the VERSION variable, to build the tagged version from git.
# 2. Run: docker login -u avaxgenesis
# 3. Make sure your ssh-agent has a key that can read the repo (ssh-add -l).
# 4. Run: ./buildx.sh
#
# That's it!

USER=avaxgenesis
REPO=skuld
VERSION=v0.7.10
IMAGE=${REPO}
BUILDER_CONTAINER_NAME=${REPO}-docker-builder
PLATFORM="linux/amd64,linux/arm64"

# The build clones the repo over ssh, using the forwarded ssh-agent. If no agent
# is reachable (e.g. SSH_AUTH_SOCK is unset or stale), start a temporary one.
ssh-add -l >/dev/null 2>&1
if [[ $? -eq 2 ]]; then
	echo "No usable ssh-agent found, starting a temporary one."
	eval "$(ssh-agent -s)" >/dev/null
	trap 'ssh-agent -k >/dev/null' EXIT
fi
if ! ssh-add -l >/dev/null 2>&1; then
	ssh-add || { echo "Could not load an ssh key into the agent."; exit 1; }
fi

# Update the version in the compile script.
sed -i "s/^VERSION=.*/VERSION=${VERSION}/g" build/compile.sh

# Create the builder container if it doesn't exist
if [[ $(docker container ls -a | grep ${BUILDER_CONTAINER_NAME} | wc -l) -eq 1 ]]; then
	echo "Builder container exists, skipping creation."
else
	docker buildx create --name ${BUILDER_CONTAINER_NAME}
	docker buildx use ${BUILDER_CONTAINER_NAME}
	docker buildx inspect --bootstrap
fi

# Build and push the images
docker buildx build --platform=${PLATFORM} \
	--tag ${USER}/${IMAGE}:${VERSION} \
	--tag ${USER}/${IMAGE}:latest \
	--ssh default \
	-f Dockerfile \
	--push \
	.
