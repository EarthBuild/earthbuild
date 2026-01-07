# Renovate Testing Scripts

This directory contains scripts for testing the Renovate configuration locally before deploying to production.

## 📋 Available Scripts

### `test-renovate-simple.sh` ⚡ (Recommended)

**Quick comprehensive testing without GitHub API access**

- ✅ **No GitHub token required**
- ✅ **Tests all configuration aspects**
- ✅ **Validates JSON5 syntax**
- ✅ **Verifies all managers and custom patterns**
- ⏱️ **~30 seconds runtime**

```bash
# From project root
./scripts/test-renovate-simple.sh

# Or from scripts directory
cd scripts && ./test-renovate-simple.sh
```

### `test-renovate-full.sh` 🚀 (Advanced)

**Full end-to-end testing with GitHub API access**

- 🔑 **Requires GitHub token**
- ✅ **Tests actual GitHub API connectivity**
- ✅ **Validates repository access**
- ✅ **Performs complete dependency scanning**
- ⏱️ **2-5 minutes runtime**

```bash
# First, get a GitHub token:
# 1. Go to GitHub.com > Settings > Developer settings > Personal access tokens
# 2. Generate new token with 'repo' scope
# 3. Export it:
export GITHUB_TOKEN=ghp_your_token_here

# Then run the full test:
./scripts/test-renovate-full.sh
```

## 🎯 What Gets Tested

Both scripts validate the comprehensive Renovate configuration for:

### **Core Managers**
- **Earthfiles**: Version variables with renovate comments
- **GitHub Actions**: Workflow action versions
- **Node.js**: package.json dependencies
- **Python**: requirements.txt packages
- **Ruby**: Gemfile dependencies
- **Docker**: Dockerfile base images
- **Docker Compose**: Service image versions

### **Custom Patterns**
- Shell script dependency downloads
- Documentation version references
- Tool version management (.mise.toml)

### **Configuration Features**
- JSON5 syntax validation
- Manager enablement
- Custom regex patterns
- Package grouping rules
- Update scheduling
- Branch-specific rules

## 📊 Expected Test Results

### ✅ Success Indicators
- `CONFIG_VALIDATION_PASSED`
- `JSON5_SYNTAX_VALID`
- `ALL_MANAGERS_ENABLED`
- `CUSTOM_PATTERNS_WORKING`

### ⚠️ Common Issues
- **JSON5 syntax errors**: Check `.github/renovate.json5` formatting
- **GitHub token invalid**: Verify token has 'repo' scope
- **Docker not found**: Install Docker for testing
- **Rate limits**: Wait and retry with different token

## 🚀 Production Deployment

After successful testing:

1. **Commit and push** all renovate changes
2. **Monitor first run**: Check repository Issues for onboarding PR
3. **Review schedule**: Updates run Monday 4pm UTC
4. **Check Actions**: Monitor Renovate workflow executions

## 📚 Configuration Overview

The Renovate setup provides:

- **🔄 Automated dependency updates** across all technology stacks
- **📅 Smart scheduling**: Monthly grouped updates
- **🎯 Intelligent grouping**: Related dependencies update together
- **⚡ Auto-merge**: Critical earthly/earthly updates merge automatically
- **🛡️ Security**: OpenSSF Scorecard integration maintained

## 💡 Testing Tips

- **Start simple**: Use `test-renovate-simple.sh` first
- **Debug mode**: Set `LOG_LEVEL=debug` for detailed output
- **Dry run safety**: Both scripts run in dry-run mode by default
- **Incremental testing**: Test individual components as you build config

## 🔗 Useful Links

- [Renovate Documentation](https://docs.renovatebot.com/)
- [Local Testing Guide](https://docs.renovatebot.com/examples/self-hosting/)
- [Custom Manager Guide](https://docs.renovatebot.com/modules/manager/regex/)
- [Configuration Options](https://docs.renovatebot.com/configuration-options/)

---

**Pro tip**: Run `test-renovate-simple.sh` after any configuration changes to ensure everything still works! 🎯
