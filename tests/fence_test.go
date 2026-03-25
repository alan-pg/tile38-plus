package tests

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/tidwall/gjson"
)

func subTestFence(g *testGroup) {

	// Standard
	g.regSubTest("basic", fence_basic_test)
	g.regSubTest("channel message order", fence_channel_message_order_test)
	g.regSubTest("detect inside,outside", fence_detect_inside_test)

	// Roaming
	g.regSubTest("roaming live", fence_roaming_live_test)
	g.regSubTest("roaming channel", fence_roaming_channel_test)
	g.regSubTest("roaming webhook", fence_roaming_webhook_test)

	// channel meta
	g.regSubTest("channel meta", fence_channel_meta_test)

	// various
	g.regSubTest("detect eecio", fence_eecio_test)

	// new features
	g.regSubTest("speed limit testing", fence_speedlimit_test)
	g.regSubTest("newer keyword testing", fence_newer_test)
}

type fenceReader struct {
	conn net.Conn
	rd   *bufio.Reader
}

func (fr *fenceReader) receive() (string, error) {
	if err := fr.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return "", err
	}
	line, err := fr.rd.ReadBytes('\n')
	if err != nil {
		return "", err
	}
	if len(line) < 4 || line[0] != '$' || line[len(line)-2] != '\r' || line[len(line)-1] != '\n' {
		return "", errors.New("invalid message")
	}
	n, err := strconv.ParseUint(string(line[1:len(line)-2]), 10, 64)
	if err != nil {
		return "", err
	}
	buf := make([]byte, int(n)+2)
	_, err = io.ReadFull(fr.rd, buf)
	if err != nil {
		return "", err
	}
	if buf[len(buf)-2] != '\r' || buf[len(buf)-1] != '\n' {
		return "", errors.New("invalid message")
	}
	js := buf[:len(buf)-2]
	var m interface{}
	if err := json.Unmarshal(js, &m); err != nil {
		return "", err
	}
	return string(js), nil
}

func (fr *fenceReader) receiveExpect(valex ...string) error {
	s, err := fr.receive()
	if err != nil {
		return err
	}
	for i := 0; i < len(valex); i += 2 {
		if gjson.Get(s, valex[i]).String() != valex[i+1] {
			return fmt.Errorf("expected '%s'='%s', got '%s'", valex[i], valex[i+1], gjson.Get(s, valex[i]).String())
		}
	}
	return nil
}

func fence_basic_test(mc *mockServer) error {
	conn, err := net.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "NEARBY mykey FENCE POINT 33 -115 5000\r\n")
	if err != nil {
		return err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	res := string(buf[:n])
	if res != "+OK\r\n" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}
	rd := &fenceReader{conn, bufio.NewReader(conn)}

	// send a point
	c, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer c.Close()

	res, err = redis.String(c.Do("SET", "mykey", "myid1", "POINT", 33, -115))
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}

	// receive the message
	if err := rd.receiveExpect("command", "set",
		"detect", "enter",
		"key", "mykey",
		"id", "myid1",
		"object.type", "Point",
		"object.coordinates", "[-115,33]"); err != nil {
		return err
	}

	if err := rd.receiveExpect("command", "set",
		"detect", "inside",
		"key", "mykey",
		"id", "myid1",
		"object.type", "Point",
		"object.coordinates", "[-115,33]"); err != nil {
		return err
	}

	res, err = redis.String(c.Do("SET", "mykey", "myid1", "POINT", 34, -115))
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}

	// receive the message
	if err := rd.receiveExpect("command", "set",
		"detect", "exit",
		"key", "mykey",
		"id", "myid1",
		"object.type", "Point",
		"object.coordinates", "[-115,34]"); err != nil {
		return err
	}

	if err := rd.receiveExpect("command", "set",
		"detect", "outside",
		"key", "mykey",
		"id", "myid1",
		"object.type", "Point",
		"object.coordinates", "[-115,34]"); err != nil {
		return err
	}
	return nil
}

func fence_channel_message_order_test(mc *mockServer) error {
	// Create a channel to store the goroutines error
	finalErr := make(chan error)

	var ready atomic.Bool
	// Concurrently subscribe for notifications
	go func() {
		// Create the subscription connection to Tile38 to subscribe for updates
		sc, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
		if err != nil {
			log.Println(err)
			return
		}
		defer sc.Close()

		// Subscribe the subscription client to the * pattern
		psc := redis.PubSubConn{Conn: sc}
		if err := psc.PSubscribe("*"); err != nil {
			log.Println(err)
			return
		}
		var msgs []string
		// While not a permanent error on the connection.
	loop:
		for sc.Err() == nil {
			switch v := psc.Receive().(type) {
			case redis.Message:
				if v.Channel == "status" && string(v.Data) == "ready" {
					ready.Store(true)
				} else {
					msgs = append(msgs, string(v.Data))
					if len(msgs) == 8 {
						break loop
					}
				}
			case error:
				fmt.Printf("%s\n", err.Error())
			}
		}
		// Verify all messages
		correctOrder := []string{"exit:A", "exit:B", "outside:A", "outside:B", "enter:C", "enter:D", "inside:C", "inside:D"}
		for i := range msgs {
			if gjson.Get(msgs[i], "detect").String()+":"+
				gjson.Get(msgs[i], "hook").String() != correctOrder[i] {
				finalErr <- errors.New("INVALID MESSAGE ORDER")
			}
		}
		finalErr <- nil
	}()
	// Create the base connection for setting up points and geofences
	bc, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer bc.Close()

	for !ready.Load() {
		if _, err := do(bc, "PUBLISH status ready"); err != nil {
			return err
		}
	}

	// Fire all setup commands on the base client
	for _, cmd := range []string{
		"SET points point POINT 33.412529053733444 -111.93368911743164",
		`SETCHAN A WITHIN points FENCE OBJECT {"type":"Polygon","coordinates":[[[-111.95205688476562,33.400491820565236],[-111.92630767822266,33.400491820565236],[-111.92630767822266,33.422272258866045],[-111.95205688476562,33.422272258866045],[-111.95205688476562,33.400491820565236]]]}`,
		`SETCHAN B WITHIN points FENCE OBJECT {"type":"Polygon","coordinates":[[[-111.93952560424803,33.403501285221594],[-111.92630767822266,33.403501285221594],[-111.92630767822266,33.41997983836345],[-111.93952560424803,33.41997983836345],[-111.93952560424803,33.403501285221594]]]}`,
		`SETCHAN C WITHIN points FENCE OBJECT {"type":"Polygon","coordinates":[[[-111.9255781173706,33.40342963251261],[-111.91201686859131,33.40342963251261],[-111.91201686859131,33.41994401881284],[-111.9255781173706,33.41994401881284],[-111.9255781173706,33.40342963251261]]]}`,
		`SETCHAN D WITHIN points FENCE OBJECT {"type":"Polygon","coordinates":[[[-111.92562103271484,33.40063513076968],[-111.90021514892578,33.40063513076968],[-111.90021514892578,33.42212898435788],[-111.92562103271484,33.42212898435788],[-111.92562103271484,33.40063513076968]]]}`,
		"SET points point POINT 33.412529053733444 -111.91909790039062",
	} {
		if _, err := do(bc, cmd); err != nil {
			return err
		}
	}
	return <-finalErr
}

func fence_detect_inside_test(mc *mockServer) error {
	conn, err := net.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = fmt.Fprintf(conn, "WITHIN users FENCE DETECT inside,outside POINTS BOUNDS 33.618824 -84.457973 33.654359 -84.399859\r\n")
	if err != nil {
		return err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	res := string(buf[:n])
	if res != "+OK\r\n" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}
	rd := &fenceReader{conn, bufio.NewReader(conn)}

	// send a point
	c, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer c.Close()

	res, err = redis.String(c.Do("SET", "users", "200", "POINT", "33.642301", "-84.43118"))
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}

	if err := rd.receiveExpect("command", "set",
		"detect", "inside",
		"key", "users",
		"id", "200",
		"point", `{"lat":33.642301,"lon":-84.43118}`); err != nil {
		return err
	}

	res, err = redis.String(c.Do("SET", "users", "200", "POINT", "34.642301", "-84.43118"))
	if err != nil {
		return err
	}
	if res != "OK" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}

	// receive the message
	if err := rd.receiveExpect("command", "set",
		"detect", "outside",
		"key", "users",
		"id", "200",
		"point", `{"lat":34.642301,"lon":-84.43118}`); err != nil {
		return err
	}
	return nil
}

// do performs the passed command on the passed redis client
func do(c redis.Conn, cmd string) (interface{}, error) {
	// Split out all parameters
	params := strings.Split(cmd, " ")

	// Produce a slice of interfaces for use in the arguments
	var args []interface{}
	for _, p := range params[1:] {
		args = append(args, p)
	}

	// Perform the request and return the response
	return c.Do(params[0], args...)
}

func fence_channel_meta_test(mc *mockServer) error {
	return mc.DoBatch([][]interface{}{
		{"SETCHAN", "carbon", "NEARBY", "x", "MATCH", "carbon*", "FENCE", "NODWELL", "points", "ROAM", "x", "*", "200000"}, {"1"},
		{"OUTPUT", "json"}, {`{"ok":true}`},
		// check for valid json on the chans command
		{"CHANS", "*"}, {
			func(v interface{}) (resp, expect interface{}) {
				// v is the value as strings or slices of strings
				// test will pass as long as `resp` and `expect` are the same.
				if !json.Valid([]byte(v.(string))) {
					return v, "Valid JSON"
				}
				return true, true
			},
		},
	})
}

func dialTile38(port int) (redis.Conn, error) {
	conn, err := redis.Dial("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, err
	}
	if _, err := conn.Do("OUTPUT", "json"); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func doTile38(c redis.Conn, cmd string, args ...interface{}) (string, error) {
	js, err := redis.String(c.Do(cmd, args...))
	if !gjson.Get(js, "ok").Bool() {
		return "", errors.New(gjson.Get(js, "err").String())
	}
	return js, err
}

func fence_eecio_test(mc *mockServer) error {
	// simulates issue #578
	var wg sync.WaitGroup
	wg.Add(3)
	ch := make(chan bool)
	var err1, err2, err3 error
	var msgs1, msgs2 []string
	// terminal 1
	go func() {
		defer wg.Done()
		err1 = func() error {
			conn, err := dialTile38(mc.port)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = doTile38(conn,
				"SETCHAN", "test-eec", "NEARBY", "fleet",
				"FENCE", "DETECT", "enter,exit,cross",
				"POINT", "10.000", "10.000", "10000")
			if err != nil {
				return err
			}
			_, err = doTile38(conn, "SUBSCRIBE", "test-eec")
			if err != nil {
				return err
			}
			ch <- true
			for {
				js, err := redis.String(conn.Receive())
				if err != nil {
					return err
				}
				if js == `"DONE"` {
					break
				}
				msgs1 = append(msgs1, js)
			}
			return nil
		}()
	}()
	// terminal 2
	go func() {
		defer wg.Done()
		err2 = func() error {
			conn, err := dialTile38(mc.port)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = doTile38(conn,
				"SETCHAN", "test-eecio", "NEARBY", "fleet",
				"FENCE", "DETECT", "enter,exit,cross,inside,outside",
				"POINT", "10.000", "10.000", "10000")
			if err != nil {
				return err
			}
			_, err = doTile38(conn, "SUBSCRIBE", "test-eecio")
			if err != nil {
				return err
			}
			ch <- true
			for {
				js, err := redis.String(conn.Receive())
				if err != nil {
					return err
				}
				if js == `"DONE"` {
					break
				}
				msgs2 = append(msgs2, js)
			}
			return nil
		}()
	}()
	// terminal 3
	var ok bool
	go func() {
		defer wg.Done()
		err3 = func() error {
			<-ch // terminal 1
			<-ch // terminal 2
			conn, err := dialTile38(mc.port)
			if err != nil {
				return err
			}
			defer conn.Close()
			if _, err = doTile38(conn,
				"SET", "fleet", "vehicle_1",
				"POINT", "10.0", "10.0"); err != nil {
				return err
			}
			if _, err = doTile38(conn,
				"SET", "fleet", "vehicle_1",
				"POINT", "0.0", "0.0"); err != nil {
				return err
			}
			if _, err = doTile38(conn,
				"SET", "fleet", "vehicle_1",
				"POINT", "20.0", "20.0"); err != nil {
				return err
			}
			if _, err = doTile38(conn, "PUBLISH", "test-eecio",
				"DONE"); err != nil {
				return err
			}
			if _, err = doTile38(conn, "PUBLISH", "test-eec",
				"DONE"); err != nil {
				return err
			}
			ok = true
			return nil
		}()
	}()
	var timeok atomic.Bool
	go func() {
		time.Sleep(time.Second * 30)
		if !timeok.Load() {
			panic("timeout")
		}
	}()
	wg.Wait()
	timeok.Store(true)
	if err3 != nil {
		return err3
	}
	if !ok {
		if err2 != nil {
			return err2
		}
		if err1 != nil {
			return err1
		}
	}
	var detects []string
	for i := 0; i < len(msgs1); i++ {
		detects = append(detects, gjson.Get(msgs1[i], "detect").String())
	}
	if strings.Join(detects, ",") != "enter,exit,cross" {
		errmsg := fmt.Sprintf("expected 'enter,exit,cross', got '%s'\n",
			strings.Join(detects, ","))
		return errors.New(errmsg)
	}
	detects = nil
	for i := 0; i < len(msgs2); i++ {
		detects = append(detects, gjson.Get(msgs2[i], "detect").String())
	}

	if strings.Join(detects, ",") != "enter,inside,exit,outside,cross,outside" {
		errmsg := fmt.Sprintf(
			"expected 'enter,inside,exit,outside,cross,outside', got '%s'\n",
			strings.Join(detects, ","))
		return errors.New(errmsg)
	}

	return nil
}

func fence_speedlimit_test(mc *mockServer) error {
	conn, err := net.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer conn.Close()

	// Add the geofence with a speed limit
	_, err = fmt.Fprintf(conn, "WITHIN fleet FENCE DETECT inside,outside,overspeed,underspeed SPEEDLIMIT 55.0 speed POINTS BOUNDS 33.618 -84.458 33.654 -84.399\r\n")
	if err != nil {
		return err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	res := string(buf[:n])
	if res != "+OK\r\n" {
		return fmt.Errorf("expected OK, got '%v'", res)
	}

	rd := &fenceReader{conn, bufio.NewReader(conn)}

	// Setup normal redis connection
	c, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer c.Close()

	// 1: Normal speed, entering fence
	resStr, err := redis.String(c.Do("SET", "fleet", "truck1", "FIELD", "speed", 45.0, "POINT", "33.642", "-84.431"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Should receive inside but NO overspeed because speed=45.0 (limit=55.0)
	if err := rd.receiveExpect("command", "set", "detect", "inside", "id", "truck1"); err != nil {
		return err
	}

	// 2: Speed up past limit, inside fence
	resStr, err = redis.String(c.Do("SET", "fleet", "truck1", "FIELD", "speed", 65.0, "POINT", "33.642", "-84.431"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Should receive both inside and overspeed
	if err := rd.receiveExpect("command", "set", "detect", "inside", "id", "truck1"); err != nil {
		return err
	}
	if err := rd.receiveExpect("command", "set", "detect", "overspeed", "id", "truck1"); err != nil {
		return err
	}

	// 3: Slow down below limit, inside fence
	resStr, err = redis.String(c.Do("SET", "fleet", "truck1", "FIELD", "speed", 50.0, "POINT", "33.642", "-84.431"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Should receive both inside and underspeed
	if err := rd.receiveExpect("command", "set", "detect", "inside", "id", "truck1"); err != nil {
		return err
	}
	if err := rd.receiveExpect("command", "set", "detect", "underspeed", "id", "truck1"); err != nil {
		return err
	}

	// 4: Set new vehicle directly over limit
	resStr, err = redis.String(c.Do("SET", "fleet", "truck2", "FIELD", "speed", 70.0, "POINT", "33.643", "-84.432"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Should receive inside AND overspeed right away
	if err := rd.receiveExpect("command", "set", "detect", "inside", "id", "truck2"); err != nil {
		return err
	}
	if err := rd.receiveExpect("command", "set", "detect", "overspeed", "id", "truck2"); err != nil {
		return err
	}

	return nil
}

func fence_newer_test(mc *mockServer) error {
	c, err := redis.Dial("tcp", fmt.Sprintf(":%d", mc.port))
	if err != nil {
		return err
	}
	defer c.Close()

	// Initial insert (base timestamp 100)
	resStr, err := redis.String(c.Do("SET", "fleet", "bus1", "FIELD", "ts", 100.0, "POINT", "33", "-115"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Update with NEWER timestamp 110.0 (Should prosper)
	resStr, err = redis.String(c.Do("SET", "fleet", "bus1", "FIELD", "ts", 110.0, "NEWER", "ts", "POINT", "33.1", "-115.1"))
	if err != nil {
		return err
	}
	if resStr != "OK" {
		return fmt.Errorf("expected OK, got '%v'", resStr)
	}

	// Out-of-order update with NEWER timestamp 105.0 (Should be silently ignored/failing but return success logic in JSON/false otherwise)
	// OutputType RESP integer evaluates the silent success block to Integer 0 (instead of OK).
	resInt, err := redis.Int(c.Do("SET", "fleet", "bus1", "FIELD", "ts", 105.0, "NEWER", "ts", "POINT", "33.2", "-115.2"))
	if err != nil {
		return err
	}
	if resInt != 0 {
		return fmt.Errorf("expected 0 for out-of-order, got '%v'", resInt)
	}

	// Verify object is at timestamp 110.0 location
	resObjStr, err := redis.String(c.Do("GET", "fleet", "bus1"))
	if err != nil {
		return err
	}

	// Expecting string serialized GeoJSON pointing at 33.1, -115.1
	if resObjStr != `{"type":"Point","coordinates":[-115.1,33.1]}` {
		return fmt.Errorf("expected bus1 to be at 33.1,-115.1, got '%v'", resObjStr)
	}
	
	// FSET out-of-order update validation
	resInt, err = redis.Int(c.Do("FSET", "fleet", "bus1", "NEWER", "ts", "ts", 108.0, "speed", 55.0))
	if err != nil {
		return err
	}
	if resInt != 0 {
		return fmt.Errorf("expected 0 for out-of-order FSET, got '%v'", resInt)
	}

	// FSET newer timestamp validation
	resInt, err = redis.Int(c.Do("FSET", "fleet", "bus1", "NEWER", "ts", "ts", 120.0, "speed", 60.0))
	if err != nil {
		return err
	}
	if resInt == 0 {
		return fmt.Errorf("expected > 0 for in-order FSET, got '%v'", resInt)
	}

	return nil
}
