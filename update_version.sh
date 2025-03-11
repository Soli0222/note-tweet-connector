#!/bin/bash

# Check if version argument is provided
if [ -z "$1" ]; then
    echo "Usage: ./update-version.sh <new_version>"
    echo "Example: ./update-version.sh 1.7.0"
    exit 1
fi

NEW_VERSION=$1

# Update main.go version
sed -i '' "s/version *= *\"[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\"/version = \"$NEW_VERSION\"/" main.go

# Update docker-compose.yaml
sed -i '' "s/note-tweet-connector:[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*/note-tweet-connector:$NEW_VERSION/" docker-compose.yaml

# Update helm chart appVersion
sed -i '' "s/appVersion: \"[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\"/appVersion: \"$NEW_VERSION\"/" helm/note-tweet-connector/Chart.yaml

# Update helm values.yaml
sed -i '' "s/tag: [0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*/tag: $NEW_VERSION/" helm/note-tweet-connector/values.yaml

echo "Version updated to $NEW_VERSION in all files"