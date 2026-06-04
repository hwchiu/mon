package agent

import (
	"context"
	"log"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/hwchiu/mon/pkg/proto/dfw/v1"
)

// GrpcClient handles communication with the controller for policy updates.
type GrpcClient struct {
	conn   *grpc.ClientConn
	client pb.DistributionClient
	zone   string
	agentID string
}

func NewGrpcClient(controllerAddr, zone, agentID string) (*GrpcClient, error) {
	conn, err := grpc.Dial(controllerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &GrpcClient{
		conn:    conn,
		client:  pb.NewDistributionClient(conn),
		zone:    zone,
		agentID: agentID,
	}, nil
}

func (c *GrpcClient) Register() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.client.Register(ctx, &pb.RegisterRequest{
		AgentId: c.agentID,
		ZoneId:  c.zone,
		// CurrentVersion: ...
	})
	return err
}

func (c *GrpcClient) StreamUpdates(handler func(*pb.PolicyUpdate)) error {
	stream, err := c.client.StreamUpdates(context.Background(), &pb.RegisterRequest{
		AgentId: c.agentID,
		ZoneId:  c.zone,
	})
	if err != nil {
		return err
	}
	for {
		update, err := stream.Recv()
		if err != nil {
			log.Printf("stream recv error: %v", err)
			return err
		}
		handler(update)
	}
}

func (c *GrpcClient) Close() {
	c.conn.Close()
}
