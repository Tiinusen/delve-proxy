package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"time"
)

var (
	localAddr  = flag.String("l", ":2345", "proxy local address")
	remoteAddr = flag.String("r", "127.0.0.1:2345", "proxy remote address")
	verbose    = flag.Bool("v", false, "test")
	cmd        *exec.Cmd
)

func startDelve() {
	stopDelve()
	cmd = exec.Command("/bin/sh", "-c", "screen -L -dmS delve dlv debug --listen "+*remoteAddr+" --headless")
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}
}

func stopDelve() {
	if cmd != nil {
		cmd.Process.Signal(os.Kill)
		time.Sleep(time.Millisecond * 100)
		cmd.Process.Release()
		cmd = nil
	}
	time.Sleep(time.Millisecond * 100)
	exec.Command("screen", "-S", "delve", "-X", "kill").Run()
	time.Sleep(time.Millisecond * 500)
	exec.Command("pkill", "-SIGKILL dlv").Run()
	time.Sleep(time.Millisecond * 500)
	exec.Command("pkill", "-SIGKILL debug").Run()
	time.Sleep(time.Millisecond * 200)
}

func main() {
	flag.Parse()

	time.Sleep(time.Millisecond * 100)
	exec.Command("rm", "screenlog.0").Run()
	exec.Command("touch", "screenlog.0").Run()
	tailer := exec.Command("tail", "-f", "screenlog.0")
	tailer.Stdout = os.Stdout
	tailer.Stderr = os.Stderr
	tailer.Start()

	laddr, err := net.ResolveTCPAddr("tcp", *localAddr)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	raddr, err := net.ResolveTCPAddr("tcp", *remoteAddr)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	var listener net.Listener
	listener, err = net.ListenTCP("tcp", laddr)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Listening on " + *localAddr)
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}

		go func(conn net.Conn) {
			if cmd != nil {
				fmt.Println("Delve is already active, closing vscode connection")
				conn.Close()
				return
			}
			var rconn net.Conn
			var err error
			fmt.Print("Starting debugger... ")
			startDelve()
			fmt.Println("done")
			fmt.Print("Waiting for debugger... ")
			for attempts := 0; attempts < 10; attempts++ {
				rconn, err = net.Dial("tcp", raddr.String())
				if err == nil {
					break
				}
				time.Sleep(time.Second * 1)
			}

			time.Sleep(time.Millisecond * 500)

			if err != nil {
				fmt.Println("failed")
				conn.Write([]byte(`{"id":1,"result":{"Threads":null,"NextInProgress":false,"exited":true,"exitStatus":0,"When":""},"error":null}`))
				time.Sleep(time.Millisecond * 100)
				conn.Close()
				stopDelve()
				return
			}
			fmt.Println("done")

			var pipeDone int32
			var timer *time.Timer

			var pipe = func(src, dst net.Conn, filter func(b *[]byte)) {
				defer func() {
					if v := atomic.AddInt32(&pipeDone, 1); v == 1 {
						timer = time.AfterFunc(time.Second*1, func() {
							if atomic.AddInt32(&pipeDone, 1) == 2 {
								conn.Close()
								rconn.Close()
								stopDelve()
							}
						})
					} else if v == 2 {
						conn.Close()
						rconn.Close()
						timer.Stop()
						stopDelve()
					}
				}()

				buff := make([]byte, 65535)
				for {
					n, err := src.Read(buff)
					if err != nil {
						return
					}
					b := buff[:n]
					if filter != nil {
						filter(&b)
					}
					n, err = dst.Write(b)
					if err != nil {
						return
					}
				}
			}
			fmt.Println("Connection established between delve and vscode")
			fmt.Println("Following output is from screenlog.0")
			go pipe(conn, rconn, nil)
			go pipe(rconn, conn, nil)
		}(conn)
	}
}
