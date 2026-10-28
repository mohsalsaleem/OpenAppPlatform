//go:build race

package sqlite

// The race detector slows the transpiled SQLite code about 270 times.
const raceBuild = true
