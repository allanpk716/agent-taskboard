package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// Operation 是一条已完成的管理操作记录。
type Operation struct {
	ID               int64
	OperationType    string
	OperatorMemberID int64
	OperatorName     string
	At               time.Time
	TicketNumbers    []string
	PackageName      string
}

// ListOperations 按平台时间倒序读取管理操作记录。
func (s *Service) ListOperations(ctx context.Context) ([]Operation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.operation_type, o.operator_member_id, m.name, o.at,
		       o.ticket_numbers, o.package_name
		FROM admin_operations o
		JOIN members m ON m.id = o.operator_member_id
		ORDER BY o.at DESC, o.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var op Operation
		var at, numbers string
		if err := rows.Scan(&op.ID, &op.OperationType, &op.OperatorMemberID, &op.OperatorName,
			&at, &numbers, &op.PackageName); err != nil {
			return nil, err
		}
		parsed, err := time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing)
		if err != nil {
			return nil, fmt.Errorf("管理操作 %d 的时间无效：%w", op.ID, err)
		}
		op.At = parsed
		op.TicketNumbers = []string{}
		if err := json.Unmarshal([]byte(numbers), &op.TicketNumbers); err != nil {
			return nil, fmt.Errorf("管理操作 %d 的工单编号列表无效：%w", op.ID, err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func insertGenerateOperation(ctx context.Context, tx *sql.Tx, operatorMemberID int64, at time.Time,
	ticketNumbers []string, packageName string) error {
	numbers, err := json.Marshal(ticketNumbers)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_operations(operation_type, operator_member_id, at, ticket_numbers, package_name)
		VALUES ('archive_generate', ?, ?, ?, ?)`,
		operatorMemberID, at.Format(time.RFC3339Nano), string(numbers), packageName)
	return err
}
