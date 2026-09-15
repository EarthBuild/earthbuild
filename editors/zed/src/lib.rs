use zed_extension_api::{self as zed, Result};

struct EarthBuildExtension;

impl zed::Extension for EarthBuildExtension {
    fn new() -> Self {
        Self
    }

    fn language_server_command(
        &mut self,
        language_server_id: &zed::LanguageServerId,
        worktree: &zed::Worktree,
    ) -> Result<zed::Command> {
        let settings =
            zed::settings::LspSettings::for_worktree(language_server_id.as_ref(), worktree)?;
        let (path, arguments, env) = match settings.binary {
            Some(binary) => (binary.path, binary.arguments, binary.env),
            None => (None, None, None),
        };
        let command = path.or_else(|| worktree.which("earth")).ok_or_else(|| {
            "earth was not found in PATH; install EarthBuild or configure lsp.earth-lsp.binary.path"
                .to_string()
        })?;

        Ok(zed::Command {
            command,
            args: arguments.unwrap_or_else(|| vec!["lsp".to_string()]),
            env: env
                .map(|values| values.into_iter().collect())
                .unwrap_or_default(),
        })
    }
}

zed::register_extension!(EarthBuildExtension);
