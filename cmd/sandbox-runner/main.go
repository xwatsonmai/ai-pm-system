package main

import (
	"ai-pm-system/internal/sandbox"
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	root := flag.String("data-root", "", "宿主机上的平台数据目录")
	socket := flag.String("socket", "/run/ai-pm-sandbox/runner.sock", "本地受限控制 socket")
	image := flag.String("image", "", "预先构建的固定 Worker 镜像")
	network := flag.String("network", "bridge", "Worker Docker 网络，不允许 host")
	uid := flag.Int("worker-uid", 1000, "Worker 非 root UID；本地开发可指定当前用户")
	gid := flag.Int("worker-gid", 1000, "Worker GID")
	timeout := flag.Duration("worker-timeout", sandbox.DefaultTimeout, "沙盒容器总时限，应大于平台 agent-total-timeout")
	flag.Parse()
	if *uid <= 0 || *gid <= 0 {
		log.Fatal("Worker 必须使用非 root 用户和组")
	}
	if *timeout <= 0 {
		log.Fatal("沙盒容器总时限必须大于 0")
	}
	r, err := sandbox.New(sandbox.Config{DataRoot: *root, Image: *image, Network: *network, UID: *uid, GID: *gid, Timeout: *timeout})
	if err != nil {
		log.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(*socket), 0750); err != nil {
		log.Fatal(err)
	}
	if old, e := os.Lstat(*socket); e == nil {
		if old.Mode()&os.ModeSocket == 0 {
			log.Fatal("socket 路径已被其他文件占用")
		}
		if c, e := net.Dial("unix", *socket); e == nil {
			c.Close()
			log.Fatal("runner 已在运行")
		}
		if err = os.Remove(*socket); err != nil {
			log.Fatal(err)
		}
	}
	l, err := net.Listen("unix", *socket)
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(*socket)
	if err = os.Chmod(*socket, 0660); err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleanup, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = r.Reap(cleanup)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
	log.Print("只读沙盒 runner 已启动")
	if err = r.Serve(ctx, l); err != nil {
		log.Fatal(err)
	}
}
