package notify

import "context"

type Notifier interface {
	Name() string
	Run(ctx context.Context) error
}

type TextPublisher interface {
	PublishText(text string) error
	PublishNodeInfo() error
}
