#!/bin/bash

# Test script for the earthly counting workflow
# This simulates what the GitHub Action will do

echo "🧪 Testing Earthly Count Workflow..."
echo ""

# Save current branch
CURRENT_BRANCH=$(git branch --show-current)

# Count in current branch
echo "📊 Counting in current branch ($CURRENT_BRANCH)..."
CURRENT_COUNT=$(git grep -i "earthly" | wc -l)
echo "Current branch count: $CURRENT_COUNT"
echo ""

# Get main branch count (without switching branches)
echo "📊 Counting in main branch..."
MAIN_COUNT=$(git grep -i "earthly" origin/main | wc -l)
echo "Main branch count: $MAIN_COUNT"
echo ""

# Calculate difference
DIFFERENCE=$((MAIN_COUNT - CURRENT_COUNT))
if [ $MAIN_COUNT -gt 0 ]; then
    PERCENTAGE=$(echo "scale=2; $DIFFERENCE * 100 / $MAIN_COUNT" | bc)
else
    PERCENTAGE=0
fi

echo "📈 Summary:"
echo "  Main branch:    $MAIN_COUNT occurrences"
echo "  Current branch: $CURRENT_COUNT occurrences"
echo "  Difference:     $DIFFERENCE"
echo "  Percentage:     $PERCENTAGE%"
echo ""

if [ $DIFFERENCE -gt 0 ]; then
    echo "✅ Great! You've reduced 'earthly' occurrences by $DIFFERENCE ($PERCENTAGE%)"
elif [ $DIFFERENCE -lt 0 ]; then
    echo "⚠️  Warning: 'earthly' occurrences have increased by ${DIFFERENCE#-}"
else
    echo "➖ No change in 'earthly' occurrences"
fi