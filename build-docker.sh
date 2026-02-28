#!/bin/bash
set -e

# Define image name and version tag
IMAGE_NAME="tile38-plus"
VERSION="1.0.0-custom"

# Set Go environment variable to build for linux (in case this is run on Mac/Windows)
export GOOS=linux

echo "=============================================="
echo " Building Tile38-Plus Linux Packages"
echo "=============================================="

# Compile the packages specifically for Linux AMD64 and ARM64
echo "Building for Linux AMD64..."
./scripts/package.sh Linux linux amd64
echo "Building for Linux ARM64..."
./scripts/package.sh ARM64 linux arm64

echo "=============================================="
echo " Building Docker Image (Linux AMD64)"
echo "=============================================="

# Build the Docker image for Linux AMD64
# (AWS ECS/EKS default to x86_64/AMD64 unless running Graviton instances)
docker build \
  --build-arg VERSION="${VERSION}" \
  --build-arg TARGETOS="linux" \
  --build-arg TARGETARCH="amd64" \
  -t ${IMAGE_NAME}:latest \
  -t ${IMAGE_NAME}:${VERSION} \
  .

echo "=============================================="
echo " Docker image built successfully!"
echo " Image Name: ${IMAGE_NAME}:latest"
echo "=============================================="
echo ""
echo "To test locally:"
echo "docker run -p 9851:9851 -v \$(pwd)/data:/data -d ${IMAGE_NAME}:latest"
echo ""
echo "To tag and push to AWS ECR:"
echo "1. aws ecr get-login-password --region <region> | docker login --username AWS --password-stdin <aws_account_id>.dkr.ecr.<region>.amazonaws.com"
echo "2. docker tag ${IMAGE_NAME}:latest <aws_account_id>.dkr.ecr.<region>.amazonaws.com/${IMAGE_NAME}:latest"
echo "3. docker push <aws_account_id>.dkr.ecr.<region>.amazonaws.com/${IMAGE_NAME}:latest"
