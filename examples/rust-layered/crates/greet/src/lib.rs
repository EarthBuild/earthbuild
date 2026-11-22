use std::io::Write;

use termcolor::{ColorChoice, StandardStream};

/// Writes the greeting, in whatever colour the terminal will take.
pub fn greeting() -> String {
    let mut out = StandardStream::stdout(ColorChoice::Never);
    let _ = out.flush();

    "hello from a layered build".to_string()
}
