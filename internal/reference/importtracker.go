package reference

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/EarthBuild/earthbuild/conslogging"
)

// ImportTrackerVal is used to resolve imports.
type ImportTrackerVal struct {
	fullPath        string
	allowPrivileged bool
}

// ImportTracker is a resolver which also takes into account imports.
type ImportTracker struct {
	local  map[string]ImportTrackerVal // local name -> import details
	global map[string]ImportTrackerVal // local name -> import details
	log    *conslogging.ConsoleLogger
}

// NewImportTracker creates a new import resolver.
func NewImportTracker(log *conslogging.ConsoleLogger, global map[string]ImportTrackerVal) *ImportTracker {
	gi := make(map[string]ImportTrackerVal)
	maps.Copy(gi, global)

	return &ImportTracker{
		local:  make(map[string]ImportTrackerVal),
		global: gi,
		log:    log,
	}
}

// Global returns the internal map of global imports.
func (ir *ImportTracker) Global() map[string]ImportTrackerVal {
	return ir.global
}

// SetGlobal sets the global import map.
func (ir *ImportTracker) SetGlobal(gi map[string]ImportTrackerVal) {
	ir.global = make(map[string]ImportTrackerVal)
	maps.Copy(ir.global, gi)
}

// Add adds an import to the resolver.
func (ir *ImportTracker) Add(importStr string, as string, global, currentlyPrivileged, allowPrivilegedFlag bool) error {
	if importStr == "" {
		return errors.New("IMPORTing empty string not supported")
	}

	aTarget := importStr + "+none" // form a fictional target for parasing purposes

	parsedImport, err := ParseTarget(aTarget)
	if err != nil {
		return fmt.Errorf("could not parse IMPORT %s: %w", importStr, err)
	}

	importStr = parsedImport.ProjectCanonical() // normalize

	var path string

	allowPrivileged := currentlyPrivileged

	switch parsedImport.Kind() {
	case KindUnspecified, KindImport, KindUnresolvedImport:
		return fmt.Errorf("IMPORT %s not supported", importStr)
	case KindRemote:
		path = parsedImport.GitURL
		allowPrivileged = allowPrivileged && allowPrivilegedFlag
	case KindLocalExternal:
		path = parsedImport.LocalPath

		if allowPrivilegedFlag {
			ir.log.Printf("the --allow-privileged flag has no effect when referencing a local target\n")
		}
	case KindLocalInternal, KindDockerfile:
		if allowPrivilegedFlag {
			ir.log.Printf("the --allow-privileged flag has no effect when referencing a local target\n")
		}
	}

	pathParts := strings.Split(path, "/")
	if len(pathParts) < 1 {
		return fmt.Errorf("IMPORT %s not supported", importStr)
	}

	defaultAs := pathParts[len(pathParts)-1]
	if defaultAs == "" {
		return fmt.Errorf("IMPORT %s not supported", importStr)
	}

	if (defaultAs == "." || defaultAs == "..") && as == "" {
		return errors.New("IMPORT requires AS if the import path ends with \".\" or \"..\"")
	}

	as = cmp.Or(as, defaultAs)

	if strings.ContainsAny(as, "/:") {
		return fmt.Errorf("invalid IMPORT AS %s", as)
	}

	if global {
		_, exists := ir.global[as]
		if exists {
			return fmt.Errorf("import ref %s already exists in this scope", as)
		}

		ir.global[as] = ImportTrackerVal{
			fullPath:        importStr,
			allowPrivileged: allowPrivileged,
		}
	} else {
		_, exists := ir.local[as]
		if exists {
			return fmt.Errorf("import ref %s already exists in this scope", as)
		}

		ir.local[as] = ImportTrackerVal{
			fullPath:        importStr,
			allowPrivileged: allowPrivileged,
		}
	}

	return nil
}

// Deref resolves the import (if any) and returns a reference with the full path.
func (ir *ImportTracker) Deref(
	ref Reference,
) (resolvedRef Reference, allowPrivileged, allowPrivilegedSet bool, err error) {
	if ref.Kind() != KindImport && ref.Kind() != KindUnresolvedImport {
		return ref, false, false, nil
	}

	resolvedImport, ok := ir.local[ref.ImportRef]
	if !ok {
		resolvedImport, ok = ir.global[ref.ImportRef]
		if !ok {
			return Reference{}, false, false, fmt.Errorf("import reference %s could not be resolved", ref.ImportRef)
		}
	}

	resolvedRefStr := fmt.Sprintf("%s+%s", resolvedImport.fullPath, ref.Name())
	if ref.IsCommand() {
		ref2, err := ParseCommand(resolvedRefStr)
		if err != nil {
			return Reference{}, false, false, err
		}

		ref2.ImportRef = ref.ImportRef
		ref2.kind = KindImport
		resolvedRef = ref2
	} else {
		ref2, err := ParseTarget(resolvedRefStr)
		if err != nil {
			return Reference{}, false, false, err
		}

		ref2.ImportRef = ref.ImportRef
		ref2.kind = KindImport
		resolvedRef = ref2
	}

	return resolvedRef, resolvedImport.allowPrivileged, true, nil
}
