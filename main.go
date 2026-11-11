package main

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed static/*
var static embed.FS

const version = "1.4.1"

func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "serve" || args[0] == "init-admin") {
		command = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("easytier-access", flag.ContinueOnError)
	configPath := flags.String("config", "", "配置文件路径")
	demo := flags.Bool("demo", false, "仅本机演示，不调用真实 EasyTier")
	port := flags.Int("port", 0, "覆盖本机监听端口")
	username := flags.String("username", "admin", "初始化的管理员名称")
	passwordStdin := flags.Bool("password-stdin", false, "从标准输入读取管理员密码")
	showVersion := flags.Bool("version", false, "显示版本")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("easytier-access " + version)
		return nil
	}
	cfg, err := loadConfig(*configPath, *demo, *port)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(cfg.DataDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(cfg.DataDir, ".manager.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("数据目录正在被另一面板进程使用；初始化或重置密码前请先停止面板")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if command == "init-admin" {
		if !*passwordStdin {
			return errors.New("请使用 --password-stdin 从标准输入设置密码，避免把密码写入命令行历史")
		}
		b, err := io.ReadAll(io.LimitReader(bufio.NewReader(os.Stdin), 4097))
		if err != nil {
			return err
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if len([]rune(password)) < 12 || len(password) > 1000 {
			return errors.New("密码须至少 12 个字符，最多 1000 字节")
		}
		name, err := cleanText(*username, "用户名", 100, true)
		if err != nil {
			return err
		}
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		store, err := openStore(cfg.DataDir)
		if err != nil {
			return err
		}
		err = store.Update(func(s *State) error {
			s.Admin = Admin{Username: name, PasswordHash: hash}
			audit(s, "admin_initialized", name, "")
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("管理员已设置。若面板正在运行，请重启面板以加载新密码并清除旧会话。")
		return nil
	}
	manager, err := newManager(cfg, nil)
	if err != nil {
		return err
	}
	if manager.Store.Snapshot().Admin.Username == "" {
		return errors.New("请先执行 init-admin --password-stdin 初始化管理员")
	}
	assets, err := fs.Sub(static, "static")
	if err != nil {
		return err
	}
	web := newWeb(manager, assets)
	server := &http.Server{Addr: fmt.Sprintf("%s:%d", cfg.Listen, cfg.Port), Handler: web, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	stop, notify := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer notify()
	go func() {
		<-stop.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if cfg.Demo {
		log.Printf("LOCAL DEMO — no real node access; login admin / demo-access-2026")
	}
	log.Printf("EasyTier Access %s listening on http://%s", version, server.Addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
