//go:build !unix

package image

import (
	"archive/tar"
	"os"
)

func applyOwner(*tar.Header, string) error          { return nil }
func applyXattrs(*tar.Header, string) error         { return nil }
func makeSpecial(*tar.Header, string) (bool, error) { return false, nil }

func readXattrs(string) (map[string]string, error) { return nil, nil }

func hardLinkID(os.FileInfo) (linkID, bool) { return linkID{}, false }
