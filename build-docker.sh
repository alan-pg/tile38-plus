#!/bin/bash
set -e

# Define image name and version tag
IMAGE_NAME="tile38-plus"
VERSION=$(git describe --tags --abbrev=0 2>/dev/null || echo "1.0.0-custom")

# Set Go environment variable to build for linux (in case this is run on Mac/Windows)
export GOOS=linux

TARGET_ARCH=${1:-"all"}

echo "=============================================="
echo " Building Tile38-Plus Linux Packages"
echo "=============================================="

if [ "$TARGET_ARCH" == "all" ] || [ "$TARGET_ARCH" == "amd64" ]; then
    # Compile the packages specifically for Linux AMD64
    echo "Building for Linux AMD64..."
    ./scripts/package.sh Linux linux amd64
    
    echo "=============================================="
    echo " Building Docker Image (Linux AMD64)"
    echo "=============================================="

    # Build the Docker image for Linux AMD64
    # (AWS ECS/EKS default to x86_64/AMD64 unless running Graviton instances)
    docker build \
      --build-arg VERSION="${VERSION}" \
      --build-arg TARGETOS="linux" \
      --build-arg TARGETARCH="amd64" \
      -t ${IMAGE_NAME}:latest-amd64 \
      -t ${IMAGE_NAME}:${VERSION}-amd64 \
      -t ${IMAGE_NAME}:latest \
      .
fi

if [ "$TARGET_ARCH" == "all" ] || [ "$TARGET_ARCH" == "arm64" ]; then
    # Compile the packages specifically for Linux ARM64
    echo "Building for Linux ARM64..."
    ./scripts/package.sh ARM64 linux arm64

    echo "=============================================="
    echo " Building Docker Image (Linux ARM64)"
    echo "=============================================="

    # Build the Docker image for Linux ARM64
    docker build \
      --build-arg VERSION="${VERSION}" \
      --build-arg TARGETOS="linux" \
      --build-arg TARGETARCH="arm64" \
      -t ${IMAGE_NAME}:latest-arm64 \
      -t ${IMAGE_NAME}:${VERSION}-arm64 \
      .
fi

echo "=============================================="
echo " Docker image built successfully!"
echo " Image Name: ${IMAGE_NAME}:latest[-amd64/-arm64]"
echo "=============================================="
echo ""
echo "To test locally:"
echo "docker run -p 9851:9851 -v \$(pwd)/data:/data -d ${IMAGE_NAME}:latest"
echo ""
echo "To tag and push to AWS ECR:"
echo "1. aws ecr get-login-password --region <region> | docker login --username AWS --password-stdin <aws_account_id>.dkr.ecr.<region>.amazonaws.com"
echo "2. docker tag ${IMAGE_NAME}:latest <aws_account_id>.dkr.ecr.<region>.amazonaws.com/${IMAGE_NAME}:latest"
echo "3. docker push <aws_account_id>.dkr.ecr.<region>.amazonaws.com/${IMAGE_NAME}:latest"
