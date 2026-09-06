package api

import "context"

// BOMRow is one line of a design's bill of materials: a unique component and
// how many times it occurs anywhere in the assembly. The v2 Manufacturing Data
// Model has no explicit quantity field, so quantity is the count of occurrences
// of that component across the whole structure (see docs/api.md).
type BOMRow struct {
	ComponentVersionID string
	Name               string
	PartNumber         string
	PartDesc           string
	Material           string
	Quantity           int
}

// GetBOM returns a flat bill of materials for the given component version: one
// row per unique sub-component, with quantity = number of occurrences. It uses
// allOccurrences (every descendant, not just immediate children) and groups by
// component version id, preserving first-seen order.
func GetBOM(ctx context.Context, token, componentVersionID string) ([]BOMRow, error) {
	all, err := allOccurrences(ctx, token, componentVersionID)
	if err != nil {
		return nil, err
	}

	idx := make(map[string]int, len(all))
	rows := make([]BOMRow, 0, len(all))
	for _, o := range all {
		cv := o.ComponentVersion
		if cv.ID == "" {
			continue
		}
		if i, ok := idx[cv.ID]; ok {
			rows[i].Quantity++
			continue
		}
		idx[cv.ID] = len(rows)
		rows = append(rows, BOMRow{
			ComponentVersionID: cv.ID,
			Name:               cv.Name,
			PartNumber:         cv.PartNumber,
			PartDesc:           cv.PartDesc,
			Material:           cv.Material,
			Quantity:           1,
		})
	}
	return rows, nil
}
