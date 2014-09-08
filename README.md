apns-go: Client for Apple Push Notification and Feedback services
=================================================================

[![GoDoc](https://godoc.org/github.com/Kwik-messenger/apns-go?status.svg)](https://godoc.org/github.com/Kwik-messenger/apns-go)

Package apns-go provides client for interacting with APNS and corresponding
Feedback service. 

Example:

```go
package main

import "github.com/Kwik-messenger/apns-go"
import "time"

func main() {
    var cert, key []byte
    // load certificate and its key, perhaps from file

    // create client
    client, err := apns.CreateClient("gateway.push.apple.com", cert, key, false)
    if err != nil {
        panic(err)
    }

    // if you want handle rejected messages, provide a callback
    var badMessageCallback apns.BadMessageCallback = func(m *apns.Message, code uint8) {}
    client.SetCallback(badMessageCallback)

    // if you need feedback client, derive it from APNS client
    // they share access credentials
    feedbackClient, err := client.SetupFeedbackClient("feedback.push.apple.com",
        time.Hour * 24)
    // poll for updates
    go func() {
        for {
            tokens, err := feedbackClient.GetBadTokens()
            // handle tokens there
        }
    }

    // start client. It will create workers who then will throw messages to APNS
    // servers.
    // note: it will take some time, depending on worker count and rtt to APNS,
    // but you can already queue messages to send
    var workerCount = 5
    err = client.Start(workerCount)
    if err != nil {
        panic(err)
    }

    // send messages
    var payload = map[string]interface{}{"aps": map[string]interface{"alert":"hello world!"},
        "data": 42}
    // token to send to
    var hexToken = "0000000000000000000000000000000000000000000000000000000000000000"
    err = client.Send(hexToken, time.Now().Add(time.Hour * 24), apns.PRIORITY_IMMEDIATE, 
        payload)
    // Send will return an error when your message is malformed - payload is too
    // large or not json.Marshall'able etc
    // however Send will succeed if token have been expired. Those messages will
    // be handled when APNS will reject them. If you set up callback, it will be
    // invoked with this message

    // gracefully stop client. It will send all remaining messages from queue
    // and wait for APNS to consume them. Then Stop will return. You must not
    // call Send after calling Stop. Otherwise it will result in panic.
    client.Stop()
}
```

Implications:

* APNS have no way to notify that message have been queued to deliver. APNS will
only complain about rejected messages. Thus, library just shoves messages into
socket and hopes for the best. Every written message also have been enqueued
into 'infligh queue'. If after specified timeout after write APNS remains silent then
message assumed delivered and have been removed from inflight queue. Otherwise
library will pop erroneous message from inflight queue and handle it. Messages further in 
that queue will be sent again.

* This best-effort delivery scheme also means that Stop will can take 
apns.WORKER_ASSUME_SENT_TIMEOUT to finish.

* Connections to APNS tend to stale and hang up. To work around this issue
workers now being respawned after apns.WORKER_IDLE_TIMEOUT. If you still
experience silent delivery fails when traffic is low, consider to lower this
timeout.
