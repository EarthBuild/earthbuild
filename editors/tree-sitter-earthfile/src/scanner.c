#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <stddef.h>

enum TokenType {
  TOKEN_EOF,
};

void *tree_sitter_earthfile_external_scanner_create(void) { return NULL; }

void tree_sitter_earthfile_external_scanner_destroy(void *payload) {
  (void)payload;
}

unsigned tree_sitter_earthfile_external_scanner_serialize(void *payload,
                                                          char *buffer) {
  (void)payload;
  (void)buffer;
  return 0;
}

void tree_sitter_earthfile_external_scanner_deserialize(void *payload,
                                                        const char *buffer,
                                                        unsigned length) {
  (void)payload;
  (void)buffer;
  (void)length;
}

bool tree_sitter_earthfile_external_scanner_scan(void *payload,
                                                TSLexer *lexer,
                                                const bool *valid_symbols) {
  (void)payload;

  if (valid_symbols[TOKEN_EOF] && lexer->lookahead == 0) {
    lexer->result_symbol = TOKEN_EOF;
    return true;
  }

  return false;
}
