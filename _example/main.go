package main

import (
	"bufio"
	"flag"
	"fmt"
	"github.com/mechmind/apns-go"
	"io"
	"io/ioutil"
	"os"
	"time"
)

const (
	GW_SANDBOX = "gateway.sandbox.push.apple.com:2195"
	GW_PRODUCT = "gateway.push.apple.com:2195"
)

var token = flag.String("token", "", "Device token")
var sandbox = flag.Bool("sandbox", false, "Token is for sandbox")
var certPath = flag.String("cert", "", "Certificate file")
var certKeyPath = flag.String("cert-key", "", "Key file for certificate")

func OnBadMessage(m *apns.Message, code uint8) {
	fmt.Println("bad message: ", apns.APNSErrors[code])
}

func main() {
	flag.Parse()

	if *token == "" {
		fmt.Println("token must be specified")
		os.Exit(1)
	}

	var gw string
	if *sandbox {
		gw = GW_SANDBOX
	} else {
		gw = GW_PRODUCT
	}

	cert, err := ioutil.ReadFile(*certPath)
	if err != nil {
		fmt.Println("cannot read certificate:", err)
		os.Exit(1)
	}

	certKey, err := ioutil.ReadFile(*certKeyPath)
	if err != nil {
		fmt.Println("cannot read certificate key:", err)
		os.Exit(1)
	}

	client, err := apns.CreateClient(gw, cert, certKey, OnBadMessage)
	if err != nil {
		fmt.Println("cannot create APNS client:", err)
		os.Exit(1)
	}

	err = client.Start()
	if err != nil {
		fmt.Println("cannot start APNS client:", err)
	}

	aps := map[string]interface{}{
		"badge": 1,
		"sound": "beep-warmguitar.aiff",
		"alert": "",
	}

	basePayload := map[string]interface{}{
		"aps":  aps,
		"from": "+79136666666",
	}

	expiry := time.Now().Add(time.Hour * 24)
	liner := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("msg> ")
		msg, _, err := liner.ReadLine()
		if err != nil {
			if err == io.EOF {
				os.Exit(0)
			}
			panic(err)
		}

		txt := string(msg)
		if txt == "" {
			continue
		}

		aps["alert"] = txt
		expiry := time.Now().Add(time.Hour * 24)
		err = client.Send(*token, int32(expiry.Unix()), apns.PRIORITY_IMMEDIATE, basePayload)
		if err != nil {
			fmt.Println("failed to send notification:", err)
		}
	}
}
