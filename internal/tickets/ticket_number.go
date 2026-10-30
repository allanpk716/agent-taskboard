package tickets

import (
	"context"
	"fmt"
)

// nextTicketNumber 在建单事务里分配当日编号。墓碑里的旧工单同样计数，
// 保证编号在工单归档移除后也永不复用（#236）。
func nextTicketNumber(ctx context.Context, q Queryer, date string) (string, error) {
	var today int
	if err := q.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM tickets WHERE number LIKE ?) +
			(SELECT COUNT(*) FROM archived_tickets WHERE number LIKE ?)`,
		date+"-%", date+"-%").Scan(&today); err != nil {
		return "", err
	}
	daily := today + 1
	if daily > 999 {
		return fmt.Sprintf("%s-%d", date, daily), nil
	}
	return fmt.Sprintf("%s-%03d", date, daily), nil
}
