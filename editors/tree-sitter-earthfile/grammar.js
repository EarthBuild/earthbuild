const commandNames = [
  "FROM DOCKERFILE",
  "SAVE ARTIFACT",
  "SAVE IMAGE",
  "GIT CLONE",
  "ELSE IF",
  "WITH DOCKER",
  "ADD",
  "ARG",
  "BUILD",
  "CACHE",
  "CATCH",
  "CMD",
  "COMMAND",
  "COPY",
  "DO",
  "ELSE",
  "END",
  "ENTRYPOINT",
  "ENV",
  "EXPOSE",
  "FINALLY",
  "FOR",
  "FROM",
  "FUNCTION",
  "HEALTHCHECK",
  "HOST",
  "IF",
  "IMPORT",
  "LABEL",
  "LET",
  "LOCALLY",
  "ONBUILD",
  "PROJECT",
  "RUN",
  "SET",
  "SHELL",
  "STOPSIGNAL",
  "TRY",
  "USER",
  "VERSION",
  "VOLUME",
  "WAIT",
  "WITH",
  "WORKDIR",
];

const commandLine = new RegExp(`(?:${commandNames.join("|")})(?:[ \\t][^\\r\\n]*)?\\r?\\n`);

// Earthfile's canonical Go lexer and parser own language semantics. This
// grammar intentionally recognizes only stable, line-oriented structure so an
// arbitrary shell argument can never consume a later target boundary.
module.exports = grammar({
  name: "earthfile",

  externals: ($) => [$._eof],

  extras: () => [],

  rules: {
    source_file: ($) => repeat(choice($.target, $.function, $.command, $.comment, $.blank_line, $.unknown_line)),

    target: ($) =>
      seq(
        field("name", $.target_name),
        ":",
        optional(field("trailing", $.arguments)),
        $._line_end,
      ),

    function: ($) =>
      choice(
        seq(
          field("name", $.function_name),
          ":",
          optional(field("trailing", $.arguments)),
          $._line_end,
        ),
        seq(
          field("name", $.explicit_function_header),
          optional(field("trailing", $.arguments)),
          $._line_end,
        ),
      ),

    explicit_function_header: (_) =>
      token(
        prec(
          40,
          /(COMMAND|FUNCTION)[ \t]+[A-Z][A-Z0-9._-]*:/,
        ),
      ),

    command: ($) => seq(optional($._indent), field("body", token(prec(30, commandLine)))),

    comment: ($) => seq(optional($._indent), /#[^\r\n]*/, $._line_end),

    blank_line: ($) => seq(optional($._indent), $._newline),

    // Unknown and continuation lines remain explicit nodes instead of errors.
    // Their contents are opaque by design.
    unknown_line: ($) => seq(optional($._indent), token(prec(-10, /[^ \t\r\n][^\r\n]*/)), $._line_end),

    target_name: (_) => token(prec(20, /[a-z][a-zA-Z0-9.-]*/)),
    function_name: (_) => token(prec(20, /[A-Z][A-Z0-9._-]*/)),
    arguments: (_) => token(/[^\r\n]+/),
    _indent: (_) => /[ \t]+/,
    _line_end: ($) => choice($._newline, $._eof),
    _newline: (_) => choice("\r\n", "\n", "\r"),
  },
});
