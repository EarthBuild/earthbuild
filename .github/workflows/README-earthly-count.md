# Earthly to Earthbuild Progress Tracker

This GitHub Action tracks the progress of renaming "earthly" to "earthbuild" throughout the repository.

## How it works

1. **Triggers on PR**: The workflow runs automatically when a pull request is opened or updated
2. **Counts occurrences**: It counts all occurrences of "earthly" (case-insensitive) in both:
   - The PR branch
   - The main branch
3. **Calculates progress**: Shows the reduction in "earthly" occurrences
4. **Posts a comment**: Adds or updates a progress report comment on the PR

## Features

- **Detailed breakdown** by file type (Go, Markdown, Earthfiles, etc.)
- **Progress visualization** with emojis and percentage calculations
- **Smart commenting**: Updates existing comments instead of creating duplicates
- **Local testing**: Run `.github/scripts/count-earthly.sh` locally for detailed analysis

## File structure

```
.github/
├── workflows/
│   └── earthly-count.yml      # Main GitHub Action workflow
└── scripts/
    ├── count-earthly.sh       # Detailed counting script
    └── test-earthly-count.sh  # Local testing script
```

## Testing locally

```bash
# Run the detailed counting script
./.github/scripts/count-earthly.sh

# Test the workflow logic
./.github/scripts/test-earthly-count.sh
```

## Example PR comment

The action will post a comment like this on PRs:

---

## 🎉 Earthly → Earthbuild Progress Report

Great progress! You've reduced "earthly" occurrences by **150** (10.5%)

### 📈 Overall Progress
| Branch | Total Count |
|--------|-------------|
| main   | 1428 |
| This PR | 1278 |
| **Difference** | **-150** (10.5%) |

### 📁 Changes by file type:
| File Type | Change |
|-----------|--------|
| Go files (.go) | ✅ -75 |
| Documentation (.md) | ✅ -50 |
| Earthfiles | ✅ -25 |

---
*Keep up the great work migrating from Earthly to Earthbuild!* 🚀