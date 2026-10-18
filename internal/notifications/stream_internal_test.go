package notifications

import "testing"

func Test待读取前多次read事件会合并且不丢notify(t *testing.T) {
	broker := newStreamBroker()
	defer broker.close()
	events, stop := broker.subscribe(7)
	defer stop()

	broker.publish(7, StreamEvent{Kind: EventKindRead, ReadIDs: []int64{1, 2}, UnreadCount: 8}, EventKindRead)
	broker.publish(7, StreamEvent{Kind: EventKindNotify}, EventKindNotify)
	broker.publish(7, StreamEvent{Kind: EventKindRead, ReadIDs: []int64{2, 3}, UnreadCount: 3}, EventKindRead)
	first := <-events
	if first.Kind != EventKindRead {
		t.Fatalf("第一帧类型 = %q, 想 read", first.Kind)
	}
	if len(first.ReadIDs) != 3 || first.ReadIDs[0] != 1 || first.ReadIDs[1] != 2 || first.ReadIDs[2] != 3 {
		t.Fatalf("合并后的 read ids = %v, 想 [1 2 3]", first.ReadIDs)
	}
	if first.UnreadCount != 3 {
		t.Fatalf("未读数 = %d, 想最后一次的 3", first.UnreadCount)
	}
	second := <-events
	if second.Kind != EventKindNotify {
		t.Fatalf("第二帧类型 = %q, 想 notify", second.Kind)
	}
}
