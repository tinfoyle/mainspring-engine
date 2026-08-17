package placement

import (
	"errors"
	"sort"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type Cell struct {
	ID               ids.CellID
	Region           string
	State            string
	AssignedAccounts int
	SoftLimit        int
}

type Assignment struct {
	AccountID           ids.AccountID
	CellID              ids.CellID
	PlacementGeneration uint64
	State               string
}

func SelectCell(cells []Cell, region string) (Cell, error) {
	eligible := make([]Cell, 0, len(cells))
	for _, cell := range cells {
		if cell.State == "active" && cell.Region == region && cell.AssignedAccounts < cell.SoftLimit {
			eligible = append(eligible, cell)
		}
	}
	if len(eligible) == 0 {
		return Cell{}, errors.New("no cell has placement capacity")
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].AssignedAccounts < eligible[j].AssignedAccounts })
	return eligible[0], nil
}
