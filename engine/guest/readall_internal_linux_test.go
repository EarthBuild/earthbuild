package guest

import "os"

// readAll is a file's contents as a string, for the tests in this directory.
func readAll(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}

	return string(b), nil
}
