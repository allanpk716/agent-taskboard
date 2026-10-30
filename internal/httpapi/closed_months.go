package httpapi

const (
	defaultClosedMonths = 3
	minClosedMonths     = 1
	maxClosedMonths     = 120
	closedMonthsMessage = "closed_months 必须是 1–120 的整数"
	closedMonthsHint    = "不传时默认 3（已关闭满 3 个月）"
)

func validClosedMonths(n int) bool {
	return n >= minClosedMonths && n <= maxClosedMonths
}
