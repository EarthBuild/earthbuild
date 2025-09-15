#!/bin/bash

# Script to count occurrences of "earthly" in the repository
# Provides detailed breakdown by file type and location

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to count occurrences in a specific file pattern
count_in_pattern() {
    local pattern="$1"
    local description="$2"
    local count=$(git grep -i "earthly" -- "$pattern" 2>/dev/null | wc -l || echo "0")
    echo "$count"
}

# Total count
total_count=$(git grep -i "earthly" 2>/dev/null | wc -l || echo "0")

echo "=== Earthly Occurrence Report ==="
echo "Total occurrences: $total_count"
echo ""

# Count by file type
echo "By file type:"
echo "  Go files (.go):        $(count_in_pattern "*.go" "Go source files")"
echo "  Markdown (.md):        $(count_in_pattern "*.md" "Documentation")"
echo "  YAML files:            $(count_in_pattern "*.yml" "YAML files") + $(count_in_pattern "*.yaml" "YAML files")"
echo "  Shell scripts (.sh):   $(count_in_pattern "*.sh" "Shell scripts")"
echo "  Earthfiles:            $(count_in_pattern "Earthfile" "Earthfiles") + $(count_in_pattern "*.earth" "Earth files")"
echo "  JSON files:            $(count_in_pattern "*.json" "JSON files")"
echo ""

# Count in key directories
echo "By directory:"
echo "  /docs:                 $(count_in_pattern "docs/*" "Documentation")"
echo "  /examples:             $(count_in_pattern "examples/*" "Examples")"
echo "  /cmd:                  $(count_in_pattern "cmd/*" "Commands")"
echo "  /tests:                $(count_in_pattern "tests/*" "Tests")"
echo "  Root directory:        $(git grep -i "earthly" --max-depth=0 2>/dev/null | wc -l || echo "0")"
echo ""

# Top 10 files with most occurrences
echo "Top 10 files with most occurrences:"
git grep -i -c "earthly" 2>/dev/null | sort -t: -k2 -rn | head -10 | while IFS=: read -r file count; do
    printf "  %-50s %s\n" "$file" "$count"
done || echo "  No files found"

# Export metrics for GitHub Actions
if [ -n "$GITHUB_OUTPUT" ]; then
    echo "total_count=$total_count" >> $GITHUB_OUTPUT
    
    # Additional metrics
    go_count=$(count_in_pattern "*.go" "Go files")
    md_count=$(count_in_pattern "*.md" "Markdown files")
    earthfile_count=$(($(count_in_pattern "Earthfile" "Earthfiles") + $(count_in_pattern "*.earth" "Earth files")))
    
    echo "go_count=$go_count" >> $GITHUB_OUTPUT
    echo "md_count=$md_count" >> $GITHUB_OUTPUT
    echo "earthfile_count=$earthfile_count" >> $GITHUB_OUTPUT
fi
