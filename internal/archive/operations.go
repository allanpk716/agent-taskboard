package archive

import (
	"context"
	"database/sql"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/adminops"
)

// Operation 是一条已完成的管理操作记录。
type Operation = adminops.Operation

// ListOperations 按平台时间倒序读取管理操作记录。
func (s *Service) ListOperations(ctx context.Context) ([]Operation, error) {
	return adminops.List(ctx, s.db)
}

func insertArchiveOperation(ctx context.Context, tx *sql.Tx, operationType string,
	operatorMemberID int64, at time.Time, ticketNumbers []string, packageName string) error {
	return adminops.InsertTx(ctx, tx, operationType, operatorMemberID, at, ticketNumbers, packageName)
}
