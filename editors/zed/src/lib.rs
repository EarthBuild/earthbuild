use zed_extension_api::{self as zed, Result};

struct EarthBuildExtension;

impl zed::Extension for EarthBuildExtension {
    fn new() -> Self {
        Self
    }

    fn language_server_command(
        &mut self,
        _language_server_id: &zed::LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        let command = worktree.which("earth").ok_or_else(|| {
            "earth was not found in PATH; install EarthBuild before enabling its language server"
                .to_string()
        })?;

        Ok(zed::Command {
            command,
            args: vec!["lsp".to_string()],
            env: Default::default(),
        })
    }
}

zed::register_extension!(EarthBuildExtension);
