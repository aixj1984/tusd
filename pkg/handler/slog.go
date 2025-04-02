package handler

import (
	"context"
	"os"

	"golang.org/x/exp/slog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// MultiHandler 是一个可以同时处理多个输出的 slog 处理程序
type MultiHandler []slog.Handler

func (m MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var err error
	for _, h := range m {
		if e := h.Handle(ctx, r); e != nil {
			err = e
		}
	}
	return err
}

func (m MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m))
	for i, h := range m {
		handlers[i] = h.WithAttrs(attrs)
	}
	return MultiHandler(handlers)
}

func (m MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m))
	for i, h := range m {
		handlers[i] = h.WithGroup(name)
	}
	return MultiHandler(handlers)
}

func GetLogHandler() *slog.Logger {
	// 配置 lumberjack 以控制日志文件的滚动
	lumberjackLogger := &lumberjack.Logger{
		Filename:   "./log/tus.log",
		MaxSize:    50,    // 每个文件最大 50MB
		MaxBackups: 30,    // 最多保留 10 个备份文件
		MaxAge:     30,    // 最多保留 28 天的日志
		Compress:   false, // 压缩旧的日志文件
	}

	// 创建文件处理程序
	fileHandler := slog.NewJSONHandler(lumberjackLogger, &slog.HandlerOptions{
		AddSource: true, // 添加代码行位置
	})

	// 创建屏幕处理程序
	consoleHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true, // 添加代码行位置
	})

	// 创建多输出处理程序
	multiHandler := MultiHandler{fileHandler, consoleHandler}

	// 设置全局的 slog 记录器
	slog.SetDefault(slog.New(multiHandler))

	return slog.New(multiHandler)

	// 设置全局的 slog 记录器
	// slog.SetDefault(slog.New(handler))
}
