package spec

// Op is the kind of a change between two trees.
type Op string

const (
	OpAdd     Op = "add"
	OpRemove  Op = "remove"
	OpReplace Op = "replace"
)

// Change is one difference between two trees. Before is nil for an add and
// After is nil for a remove.
type Change struct {
	Path   string
	Op     Op
	Before any
	After  any
}

// Diff returns the changes that turn a into b, sorted by path. Maps are
// compared key by key; lists and scalars are replaced whole.
func Diff(a, b any) []Change {
	var out []Change
	diff(a, b, "", &out)
	return out
}

func diff(a, b any, path string, out *[]Change) {
	am, aIsMap := a.(map[string]any)
	bm, bIsMap := b.(map[string]any)
	if aIsMap && bIsMap {
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range sortedKeys(keys) {
			av, inA := am[k]
			bv, inB := bm[k]
			child := Join(path, k)
			switch {
			case !inA:
				*out = append(*out, Change{Path: child, Op: OpAdd, After: bv})
			case !inB:
				*out = append(*out, Change{Path: child, Op: OpRemove, Before: av})
			default:
				diff(av, bv, child, out)
			}
		}
		return
	}
	if !Equal(a, b) {
		*out = append(*out, Change{Path: path, Op: OpReplace, Before: a, After: b})
	}
}

// Clone returns a deep copy of a tree.
func Clone(tree any) any {
	switch t := tree.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, v := range t {
			m[k] = Clone(v)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, v := range t {
			l[i] = Clone(v)
		}
		return l
	}
	return tree
}
