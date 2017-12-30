//
// Challenge server for the "v9" Chromium pwnable of 34C3 CTF.
//
// Copyright (c) 2017 Samuel Groß
//

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	dockerapi "github.com/docker/docker/client"
	"log"
	"math"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Client = bufio.ReadWriter

const (
	host = "0.0.0.0"
	port = "1337"

	containerTimeout  = 120 // in seconds
	connectionTimeout = 30 * time.Minute

	powHardness = 24 // number of leading zero bits in a sha256 hash

	greeting = `Welcome!

In this challenge you are asked to pwn a modified Chromium and read the flag from /flag. You can find the challenge files here: https://34c3ctf.ccc.ac/uploads/v9-cc3610226678027367fef6992639abc1ee6dcaed.tar.gz

This challenge will work as follows:

    1. I'll ask you for a proof-of-work

    2. I'll ask you for a URL to your exploit and try to access it

    3. I'll enqueue your URL

    4. Once it's your turn, I'll run Chromium in a fresh container and point it to your URL

    5. I'll destroy the container after %d seconds

Current length of the queue: %d

Enjoy!

`
)

var (
	workdir   string
	workQueue chan string
	docker    *dockerapi.Client
)

func main() {
	var err error

	workdir, err = os.Getwd()
	if err != nil {
		log.Fatalf("Could not determine working directory: %v", err)
	}
	log.Printf("Working directory: %s", workdir)

	docker, err = dockerapi.NewEnvClient()
	if err != nil {
		log.Fatal("Failed to create docker environment: %v", err)
	}

	workQueue = make(chan string, 1024)
	go dockerWorker()

	socket, err := net.Listen("tcp", host+":"+port)
	if err != nil {
		log.Fatalf("Error listening: %v", err)
	}
	defer socket.Close()

	log.Printf("Listening on %v", host+":"+port)

	for {
		conn, err := socket.Accept()
		if err != nil {
			log.Fatalf("Error accepting: %v", err)
		}

		log.Printf("New connection from %v", conn.RemoteAddr())

		conn.SetDeadline(time.Now().Add(connectionTimeout))

		go handleClient(conn)
	}
}

func dockerWorker() {
	for {
		url := <-workQueue
		startContainer(url)
	}
}

func startContainer(url string) {
	ctx := context.Background()

	resp, err := docker.ContainerCreate(ctx, &container.Config{
		Image: "saelo/v9",
		Cmd:   []string{"chromium-browser", "--headless", "--disable-gpu", "--no-sandbox", "--virtual-time-budget=60000", url},
	}, nil, nil, "")
	if err != nil {
		log.Printf("Failed to create container: %v", err)
		return
	}

	if err := docker.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		log.Printf("Failed to start container %s: %v", resp.ID, err)
		return
	}

	waitCtx, cancel := context.WithTimeout(ctx, containerTimeout*time.Second)
	defer cancel()

	statusCh, errCh := docker.ContainerWait(waitCtx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		log.Printf("Container %s timed out: %v", resp.ID, err)
		if err := docker.ContainerKill(ctx, resp.ID, "SIGKILL"); err != nil {
			log.Printf("Failed to kill container %s: %v", resp.ID, err)
		}
	case <-statusCh:
	}

	if err := docker.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{}); err != nil {
		log.Printf("Failed to remove container %s: %v", resp.ID, err)
	}
}

func randomString(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		log.Fatal("Failed to obtain random bytes")
	}
	return hex.EncodeToString(b)[:n]
}

func proofOfWork(client *Client) error {
	challenge := randomString(16)

	client.WriteString("Proof of work code & solver can be found at https://34c3ctf.ccc.ac/uploads/pow.py\n")
	client.WriteString(fmt.Sprintf("Your challenge is %d_%s\n", int(math.Exp2(powHardness)), challenge))
	client.WriteString("Your solution: ")
	client.Flush()

	response, err := client.Reader.ReadString('\n')
	if err != nil {
		return err
	}

	solution, err := strconv.ParseUint(strings.TrimSuffix(response, "\n"), 10, 64)
	if err != nil {
		client.WriteString("That's not a valid number... Please try again")
		return err
	}

	var buf bytes.Buffer
	buf.Write([]byte(challenge))
	binary.Write(&buf, binary.LittleEndian, solution)

	hashBytes := sha256.Sum256(buf.Bytes())
	hash := big.NewInt(0)
	hash.SetBytes(hashBytes[:])
	zeroes := strings.Repeat("0", powHardness)

	if !strings.HasPrefix(fmt.Sprintf("%0256b", hash), zeroes) {
		client.WriteString("Invalid solution... Please try again")
		return errors.New("Invalid solution")
	}

	return nil
}

func handleClient(conn net.Conn) {
	defer conn.Close()

	client := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	defer client.Flush()

	client.WriteString(fmt.Sprintf(greeting, containerTimeout, len(workQueue)))
	client.Flush()

	err := proofOfWork(client)
	if err != nil {
		return
	}

	client.WriteString("Ok\n")
	client.WriteString("Now please give me the URL to your exploit: ")
	client.Flush()

	rawUrl, err := client.ReadString('\n')
	if err != nil {
		return
	}

	url, err := url.Parse(strings.TrimSuffix(rawUrl, "\n"))
	if err != nil || !url.IsAbs() {
		client.WriteString("Hmm, this doesn't looke like a valid URL to me... Please try again")
		return
	}
	log.Printf("Got URL: %v", url)

	// Fetch the URL once to verify it is reachable
	cmd := exec.Command("wget",
		"-p",                                                             // fetch all required files
		"-k",                                                             // rewrite links
		"-P", fmt.Sprintf("%s/attempts/%d/", workdir, time.Now().Unix()), // set directory prefix
		url.String())

	if err := cmd.Start(); err != nil {
		log.Printf("Failed to start wget: %v. This is probably bad...", err)
	}

	timer := time.AfterFunc(10*time.Second, func() { cmd.Process.Kill() })
	if err := cmd.Wait(); err != nil {
		client.WriteString("Hmm, it seems I could not access your URL... Please make sure it is reachable and try again")
		return
	}
	timer.Stop()

	client.WriteString("Ok\n")
	client.WriteString("I will now enqueue your URL. You should get a visit from Chromium soon. Bye!\n")
	client.Flush()

	workQueue <- url.String()
}
