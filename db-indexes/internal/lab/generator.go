package lab

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

const DatasetEnd = "2026-01-01T00:00:00Z"

var datasetEnd = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type Transaction struct {
	SourceWalletID      int64
	DestinationWalletID int64
	Status              string
	AmountCents         int64
	CreatedAt           time.Time
	Reference           string
	Description         string
}

type Generator struct {
	random *rand.Rand
}

func NewGenerator(seed int64) *Generator {
	return &Generator{random: rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))}
}

func (generator *Generator) Next(sequence int64) Transaction {
	sourceWalletID := int64(101 + generator.random.IntN(9_900))
	if generator.random.IntN(100) < 40 {
		sourceWalletID = int64(1 + generator.random.IntN(100))
	}

	destinationWalletID := int64(1 + generator.random.IntN(10_000))
	if destinationWalletID == sourceWalletID {
		destinationWalletID = destinationWalletID%10_000 + 1
	}

	statusRoll := generator.random.IntN(100)
	status := "completed"
	if statusRoll >= 85 && statusRoll < 95 {
		status = "pending"
	} else if statusRoll >= 95 {
		status = "failed"
	}

	return Transaction{
		SourceWalletID:      sourceWalletID,
		DestinationWalletID: destinationWalletID,
		Status:              status,
		AmountCents:         int64(100 + generator.random.IntN(999_901)),
		CreatedAt:           datasetEnd.Add(-time.Duration(1 + generator.random.Int64N(int64(365*24*time.Hour)))),
		Reference:           fmt.Sprintf("txn-%09d", sequence),
		Description:         strings.Repeat(fmt.Sprintf("transfer-%09d ", sequence), 7),
	}
}
