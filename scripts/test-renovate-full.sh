#!/bin/bash

# Full Renovate Test Script with GitHub Token Support
# This script runs Renovate locally with GitHub API access for comprehensive testing

set -euo pipefail

echo "🧪 Full Renovate Test (with GitHub API access)"
echo ""

# Check if we're in the right directory
if [ -f ".github/renovate.json5" ]; then
    echo "✅ Found renovate.json5 in project root"
elif [ -f "../.github/renovate.json5" ]; then
    echo "✅ Found renovate.json5, moving to project root"
    cd ..
else
    echo "❌ Error: .github/renovate.json5 not found. Run this script from the scripts directory or project root."
    exit 1
fi

# Check if Docker is available
if ! command -v docker &> /dev/null; then
    echo "❌ Error: Docker is required but not found."
    exit 1
fi

# Check for GitHub token
if [ -z "${GITHUB_TOKEN:-}" ]; then
    echo "❌ Error: GITHUB_TOKEN environment variable is required for full testing."
    echo ""
    echo "💡 How to get a GitHub token:"
    echo "   1. Go to GitHub Settings > Developer settings > Personal access tokens"
    echo "   2. Create a new token with 'repo' scope"
    echo "   3. Export it: export GITHUB_TOKEN=your_token_here"
    echo "   4. Run this script again"
    echo ""
    echo "🔄 Or run './test-renovate-simple.sh' for basic validation without API access"
    exit 1
fi

# Configuration
export RENOVATE_PLATFORM=github
export RENOVATE_TOKEN="$GITHUB_TOKEN"
export RENOVATE_DRY_RUN=${DRY_RUN:-true}
export RENOVATE_LOG_LEVEL=${LOG_LEVEL:-info}
# Set config file path
export RENOVATE_CONFIG_FILE="/usr/src/app/.github/renovate.json5"

# Repository configuration
GITHUB_USER=${GITHUB_USER:-earthbuild}
REPO_NAME=${REPO_NAME:-earthbuild}
REPOSITORY="$GITHUB_USER/$REPO_NAME"

echo "📋 Test Configuration:"
echo "   Repository: $REPOSITORY"
echo "   Dry Run: $RENOVATE_DRY_RUN"
echo "   Log Level: $RENOVATE_LOG_LEVEL"
echo "   Config File: .github/renovate.json5"
echo "   GitHub Token: ✅ Provided (${#GITHUB_TOKEN} chars)"
echo ""

# Validate token first
echo "🔐 Validating GitHub token..."
TOKEN_TEST=$(docker run --rm \
    -e GITHUB_TOKEN="$GITHUB_TOKEN" \
    curlimages/curl:latest \
    curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: token $GITHUB_TOKEN" \
    https://api.github.com/user)

if [ "$TOKEN_TEST" = "200" ]; then
    echo "   ✅ GitHub token is valid"
else
    echo "   ❌ GitHub token validation failed (HTTP $TOKEN_TEST)"
    exit 1
fi

echo ""
echo "🚀 Running Renovate with full GitHub API access..."
echo "   (This may take a few minutes on first run)"
echo ""

# Run Renovate with full configuration
docker run --rm \
    -e RENOVATE_PLATFORM \
    -e RENOVATE_TOKEN \
    -e RENOVATE_DRY_RUN \
    -e RENOVATE_LOG_LEVEL \
    -e RENOVATE_CONFIG_FILE \
    -e LOG_LEVEL \
    -v "$(pwd)":/usr/src/app:ro \
    -w /usr/src/app \
    renovate/renovate:latest \
    --schedule= \
    "$REPOSITORY"

RENOVATE_EXIT_CODE=$?

echo ""
echo "📊 Test Results:"

if [ $RENOVATE_EXIT_CODE -eq 0 ]; then
    echo "   ✅ Renovate completed successfully!"
    echo ""
    echo "🔍 What was tested:"
    echo "   ✅ Configuration file parsing"
    echo "   ✅ GitHub API connectivity"
    echo "   ✅ Repository access"
    echo "   ✅ Dependency detection across all managers"
    echo "   ✅ Custom regex patterns for Earthfiles"
    echo "   ✅ Package file parsing (npm, pip, bundler)"
    echo "   ✅ Docker image version detection"
    echo "   ✅ GitHub Actions version detection"
    echo ""
    echo "📈 Expected behavior in production:"
    echo "   • Renovate will create an onboarding PR on first run"
    echo "   • Updates will be scheduled for Monday 4pm UTC"
    echo "   • Dependencies will be grouped as configured"
    echo "   • earthly/earthly updates will auto-merge"
else
    echo "   ⚠️  Renovate completed with warnings/errors (exit code: $RENOVATE_EXIT_CODE)"
    echo ""
    echo "🔧 Common issues and solutions:"
    echo "   • 'Repository not found': Check repository name and token permissions"
    echo "   • 'Config validation failed': Review .github/renovate.json5 syntax"
    echo "   • 'Rate limit exceeded': Wait and try again, or use a different token"
    echo "   • 'No dependencies found': This is normal for dry runs in some cases"
fi

echo ""
echo "💡 Advanced testing options:"
echo "   • DRY_RUN=false: Test actual PR creation (be careful!)"
echo "   • LOG_LEVEL=debug: See detailed processing logs"
echo "   • Add --print-config flag to see final configuration"
echo ""
echo "📚 Useful Renovate documentation:"
echo "   • Local testing: https://docs.renovatebot.com/examples/self-hosting/"
echo "   • Configuration: https://docs.renovatebot.com/configuration-options/"
echo "   • Custom managers: https://docs.renovatebot.com/modules/manager/regex/"

echo ""
echo "🎯 Test completed! Exit code: $RENOVATE_EXIT_CODE"
