package approval

import (
	"context"
	"sync"
)

// fakeBotSender implements BotSender for testing.
type fakeBotSender struct {
	mu   sync.Mutex
	sent []struct {
		chatID    int64
		text      string
		parseMode string
	}
	err error
}

func (f *fakeBotSender) Send(ctx context.Context, chatID int64, text string, parseMode string) (any, error) {
	f.mu.Lock()
	f.sent = append(f.sent, struct {
		chatID    int64
		text      string
		parseMode string
	}{chatID, text, parseMode})
	f.mu.Unlock()
	return nil, f.err
}

func (f *fakeBotSender) SendWithKeyboard(ctx context.Context, chatID int64, text string, parseMode string, keyboard InlineKeyboardMarkup) (any, error) {
	f.mu.Lock()
	f.sent = append(f.sent, struct {
		chatID    int64
		text      string
		parseMode string
	}{chatID, text, parseMode})
	f.mu.Unlock()
	return nil, f.err
}

func (f *fakeBotSender) getSent() []struct {
	chatID    int64
	text      string
	parseMode string
} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]struct {
		chatID    int64
		text      string
		parseMode string
	}{}, f.sent...)
}
