package metrics

import (
	"context"
	"os"
	"runtime"
	"time"

	appmetrics "github.com/accretional/anyserver/metrics"
	pb "github.com/accretional/anyserver/proto/metrics"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Service implements the Metrics gRPC service.
type Service struct {
	pb.UnimplementedMetricsServer

	port     int
	bootTime time.Time
	counter  *appmetrics.RequestCounter

	buildLog []byte // raw BuildLog binarypb
	testLog  []byte // raw TestLog binarypb
	bootLog  *pb.BootLog
}

// New creates a Metrics service.
func New(port int, counter *appmetrics.RequestCounter, buildLogPB, testLogPB []byte) *Service {
	s := &Service{
		port:     port,
		bootTime: time.Now(),
		counter:  counter,
		buildLog: buildLogPB,
		testLog:  testLogPB,
		bootLog: &pb.BootLog{
			Events: []*pb.BootEvent{
				{Status: pb.BootStatus_BOOT_UNKNOWN, Timestamp: timestamppb.Now()},
			},
		},
	}
	return s
}

// RecordBootStarted records that boot has started.
func (s *Service) RecordBootStarted() {
	s.bootLog.Events = append(s.bootLog.Events, &pb.BootEvent{
		Status:    pb.BootStatus_BOOT_STARTED,
		Timestamp: timestamppb.Now(),
	})
}

// RecordBootComplete records that boot is complete.
func (s *Service) RecordBootComplete() {
	s.bootLog.Events = append(s.bootLog.Events, &pb.BootEvent{
		Status:    pb.BootStatus_BOOT_COMPLETE,
		Timestamp: timestamppb.Now(),
	})
}

func (s *Service) Static(ctx context.Context, req *pb.StaticRequest) (*pb.StaticResponse, error) {
	hostname, _ := os.Hostname()

	resp := &pb.StaticResponse{
		Hostname:  hostname,
		Port:      int32(s.port),
		GoVersion: runtime.Version(),
		Os:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		BootLog:   s.bootLog,
	}

	// Unmarshal embedded build/test logs
	if len(s.buildLog) > 0 {
		var bl pb.BuildLog
		if err := proto.Unmarshal(s.buildLog, &bl); err == nil {
			resp.BuildLog = &bl
		}
	}
	if len(s.testLog) > 0 {
		var tl pb.TestLog
		if err := proto.Unmarshal(s.testLog, &tl); err == nil {
			resp.TestLog = &tl
		}
	}

	return resp, nil
}

func (s *Service) Active(ctx context.Context, req *pb.ActiveRequest) (*pb.ActiveResponse, error) {
	stats := appmetrics.GetRuntimeStats()
	return &pb.ActiveResponse{
		Goroutines:     stats.Goroutines,
		HeapAllocBytes: stats.HeapAllocBytes,
		SysBytes:       stats.SysBytes,
		NumGc:          stats.NumGC,
	}, nil
}

func (s *Service) Lifetime(ctx context.Context, req *pb.LifetimeRequest) (*pb.LifetimeResponse, error) {
	return &pb.LifetimeResponse{
		BootTime:         timestamppb.New(s.bootTime),
		UptimeSeconds:    int64(time.Since(s.bootTime).Seconds()),
		TotalRequests:    s.counter.Total(),
		RequestsByPath:   s.counter.ByPath(),
		RequestsByStatus: s.counter.ByStatus(),
	}, nil
}

func (s *Service) Historical(ctx context.Context, req *pb.HistoricalRequest) (*pb.HistoricalResponse, error) {
	return &pb.HistoricalResponse{
		Placeholder: "TODO: implement time-series data",
	}, nil
}
