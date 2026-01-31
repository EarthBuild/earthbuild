package parser

// GetLexerModeNames returns the generated mode names.
func GetLexerModeNames() []string {
	return NewEarthLexer(nil).modeNames
}

// GetLexerSymbolicNames returns the generated token names.
func GetLexerSymbolicNames() []string {
	return NewEarthLexer(nil).SymbolicNames
}

// GetLexerLiteralNames returns the generated literal names.
func GetLexerLiteralNames() []string {
	return NewEarthLexer(nil).LiteralNames
}
