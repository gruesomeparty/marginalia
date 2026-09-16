package feedback

import "sort"

// blocksFrom builds Materialize's input from the hash map and document order
// the tests were written against, before Block replaced that pair.
//
// The pair could express a state the slice cannot: a block present in hashes
// but missing from order — known to exist, yet never listed. That was a latent
// inconsistency in the old signature. Here such a block is appended rather than
// dropped, so a test that relied on it keeps its blocks instead of silently
// losing them.
func blocksFrom(hashes map[string]string, order []string) []Block {
	blocks := make([]Block, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		blocks = append(blocks, Block{ID: id, Hash: hashes[id]})
		seen[id] = true
	}
	var extra []string
	for id := range hashes {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		blocks = append(blocks, Block{ID: id, Hash: hashes[id]})
	}
	return blocks
}
