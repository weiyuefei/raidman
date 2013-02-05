package raidman

import (
	"bytes"
	pb "code.google.com/p/goprotobuf/proto"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/amir/raidman/proto"
	"net"
	"reflect"
	"sync"
)

type network interface {
	Send(message *proto.Msg, conn net.Conn) (*proto.Msg, error)
}

type tcp struct{}

type udp struct{}

// Client represents a connection to a Riemann server
type Client struct {
	m          sync.Mutex
	net        network
	connection net.Conn
}

// An Event represents a single Riemann event
type Event struct {
	Ttl         float32
	Time        int64
	Host        string
	State       string
	Service     string
	Description string
	Float       float32
	Double      float64
	Int         int64
}

// Dial establishes a connection to a Riemann server
func Dial(netwrk, addr string) (c *Client, err error) {
	c = new(Client)

	var cnet network
	switch netwrk {
	case "tcp", "tcp4", "tcp6":
		cnet = new(tcp)
	case "udp", "udp4", "udp6":
		cnet = new(udp)
	default:
		return nil, fmt.Errorf("dial %q: unsupported network %q", netwrk, netwrk)
	}

	c.net = cnet
	c.connection, err = net.Dial(netwrk, addr)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (network *tcp) Send(message *proto.Msg, conn net.Conn) (*proto.Msg, error) {
	msg := &proto.Msg{}
	data, err := pb.Marshal(message)
	if err != nil {
		return msg, err
	}
	b := new(bytes.Buffer)
	if err = binary.Write(b, binary.BigEndian, uint32(len(data))); err != nil {
		return msg, err
	}
	if _, err = conn.Write(b.Bytes()); err != nil {
		return msg, err
	}
	if _, err = conn.Write(data); err != nil {
		return msg, err
	}
	var header uint32
	if err = binary.Read(conn, binary.BigEndian, &header); err != nil {
		return msg, err
	}
	response := make([]byte, header)
	if _, err = conn.Read(response); err != nil {
		return msg, err
	}
	if err = pb.Unmarshal(response, msg); err != nil {
		return msg, err
	}
	if msg.GetOk() != true {
		return msg, errors.New(msg.GetError())
	}
	return msg, nil
}

func (network *udp) Send(message *proto.Msg, conn net.Conn) (*proto.Msg, error) {
	data, err := pb.Marshal(message)
	if err != nil {
		return nil, err
	}
	if _, err = conn.Write(data); err != nil {
		return nil, err
	}

	return nil, nil
}

func eventToPbEvent(event *Event) *proto.Event {
	var e proto.Event

	t := reflect.ValueOf(&e).Elem()
	s := reflect.ValueOf(event).Elem()
	typeOfEvent := s.Type()
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		value := reflect.ValueOf(f.Interface())
		if reflect.Zero(f.Type()) != value {
			name := typeOfEvent.Field(i).Name
			switch name {
			case "State", "Service", "Host", "Description":
				tmp := reflect.ValueOf(pb.String(value.String()))
				t.FieldByName(name).Set(tmp)
			case "Ttl":
				tmp := reflect.ValueOf(pb.Float32(float32(value.Float())))
				t.FieldByName(name).Set(tmp)
			case "Time":
				tmp := reflect.ValueOf(pb.Int64(value.Int()))
				t.FieldByName(name).Set(tmp)
			case "Float":
				tmp := reflect.ValueOf(pb.Float32(float32(value.Float())))
				t.FieldByName("MetricF").Set(tmp)
			case "Int":
				tmp := reflect.ValueOf(pb.Int64(value.Int()))
				t.FieldByName("MetricSint64").Set(tmp)
			case "Double":
				tmp := reflect.ValueOf(pb.Float64(value.Float()))
				t.FieldByName("MetricD").Set(tmp)
			}
		}
	}

	return &e
}

func pbEventsToEvents(pbEvents []*proto.Event) []Event {
	var events []Event

	for _, event := range pbEvents {
		e := Event{
			State:       event.GetState(),
			Service:     event.GetService(),
			Host:        event.GetHost(),
			Description: event.GetDescription(),
			Ttl:         event.GetTtl(),
			Time:        event.GetTime(),
			Float:       event.GetMetricF(),
			Int:         event.GetMetricSint64(),
			Double:      event.GetMetricD(),
		}
		events = append(events, e)
	}

	return events
}

// Send sends an event to to Riemann
func (c *Client) Send(event *Event) error {
	e := eventToPbEvent(event)
	message := &proto.Msg{}
	message.Events = append(message.Events, e)
	c.m.Lock()
	defer c.m.Unlock()
	_, err := c.net.Send(message, c.connection)
	if err != nil {
		return err
	}

	return nil
}

// Query returns a list of events matched by query
func (c *Client) Query(q string) ([]Event, error) {
	switch c.net.(type) {
	case *udp:
		return nil, errors.New("Querying over UDP is not supported")
	}
	query := &proto.Query{}
	query.String_ = pb.String(q)
	message := &proto.Msg{}
	message.Query = query
	c.m.Lock()
	defer c.m.Unlock()
	response, err := c.net.Send(message, c.connection)
	if err != nil {
		return nil, err
	}
	return pbEventsToEvents(response.GetEvents()), nil
}

// Close closes the connection to Riemann
func (c *Client) Close() {
	c.m.Lock()
	c.connection.Close()
	c.m.Unlock()
}
