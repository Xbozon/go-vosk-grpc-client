package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/gordonklaus/portaudio"
	"google.golang.org/grpc"

	pb "github.com/Xbozon/go-vosk-grpc-client/stt_service"
)

const address = "localhost:5001"

func main() {
	portaudio.Initialize()
	defer portaudio.Terminate()

	// If there is no selected device, print all of them and exit.
	args := os.Args[1:]
	if len(args) == 0 {
		printAvailableDevices()
		return
	}

	selectedDevice, err := selectInputDevice(args)
	if err != nil {
		log.Fatalf("select input device %s", err)
		return
	}

	// Set up connection to vosk-server
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("create connect to gRPC server %v", err)
		return
	}

	client := pb.NewSttServiceClient(conn)
	stream, err := client.StreamingRecognize(context.Background())
	if err != nil {
		log.Fatalf("open stream error %v", err)
		return
	}

	ctx := stream.Context()
	done := make(chan bool)

	// Start listening responses from vosk-server
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				close(done)
				return
			}
			if err != nil {
				log.Fatalf("can not receive %v", err)
			}

			str := resp.String()
			if !strings.Contains(str, "chunks:{alternatives:{}}") {
				log.Printf(str)
			}
		}
	}()

	// Before we start streaming audio to the vosk-server, we need to set up the audio parameters.
	// It is very important to send your sample rate data to the server in the first message.
	err = sendAudioConfig(stream, int64(selectedDevice.DefaultSampleRate))
	if err != nil {
		log.Fatalf("can't send audio config")
		return
	}

	audioCtx := context.WithoutCancel(ctx)

	// Set up the audio stream parameters for LINEAR16 PCM
	// Changing the buffer size allows you to send data to the server more often or less often.
	in := make([]int16, 512*8) // Use int16 to capture 16-bit samples
	audioStream, err := portaudio.OpenDefaultStream(
		selectedDevice.MaxInputChannels, 0, selectedDevice.DefaultSampleRate, len(in), &in,
	)
	if err != nil {
		log.Fatalf("Error opening stream: %v", err)
		return
	}

	// Start the audio stream
	if err := audioStream.Start(); err != nil {
		log.Fatalf("Error starting stream: %v", err)
		return
	}

	go func() {
		for {
			select {
			case <-audioCtx.Done():
				if err := audioStream.Close(); err != nil {
					log.Println(err)
				}
				fmt.Println("Get audioCtx.Done exit gracefully...")
				return
			default:
				// Read from the microphone
				if err := audioStream.Read(); err != nil {
					log.Fatalf("Error reading from stream: %v", err)
				}

				audio := &pb.StreamingRecognitionRequest{
					StreamingRequest: &pb.StreamingRecognitionRequest_AudioContent{
						AudioContent: int16SliceToByteSlice(in),
					},
				}
				if err := stream.Send(audio); err != nil {
					log.Fatalf("can not send %v", err)
				}
			}
		}
	}()

	// Shutdown.
	go func() {
		<-ctx.Done()
		if err := ctx.Err(); err != nil {
			log.Println(err)
		}
		close(done)
		err := stream.CloseSend()
		if err != nil {
			log.Fatalf("stream.CloseSend %v", err)
			return
		}
	}()

	<-done
	log.Println("finished")
}

func printAvailableDevices() {
	devices, err := portaudio.Devices()
	if err != nil {
		log.Fatalf("portaudio.Devices %s", err)
		return
	}
	for i, device := range devices {
		fmt.Printf(
			"ID: %d, Name: %s, MaxInputChannels: %d, Sample rate: %f\n",
			i,
			device.Name,
			device.MaxInputChannels,
			device.DefaultSampleRate,
		)
	}
}

func selectInputDevice(args []string) (*portaudio.DeviceInfo, error) {
	deviceID, err := strconv.Atoi(args[0])
	if err != nil {
		return nil, fmt.Errorf("parce int %w", err)
	}

	devices, err := portaudio.Devices()
	if err != nil {
		return nil, fmt.Errorf("select input device %w", err)
	}

	selectedDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("find default device %w", err)
	}

	// Set default device to device with particular id
	selectedDevice = devices[deviceID]

	return selectedDevice, nil
}

func sendAudioConfig(stream pb.SttService_StreamingRecognizeClient, sampleRate int64) error {
	req := &pb.StreamingRecognitionRequest{
		StreamingRequest: &pb.StreamingRecognitionRequest_Config{
			Config: &pb.RecognitionConfig{
				Specification: &pb.RecognitionSpec{
					AudioEncoding:         pb.RecognitionSpec_LINEAR16_PCM, // for example, LINEAR16_PCM
					SampleRateHertz:       sampleRate,                      // the sample rate of the audio
					PartialResults:        true,
					EnableWordTimeOffsets: true,
					MaxAlternatives:       1,
				},
			},
		},
	}
	if err := stream.Send(req); err != nil {
		return err
	}
	return nil
}

func int16SliceToByteSlice(ints []int16) []byte {
	// This function converts a slice of int16s to a slice of bytes
	size := len(ints) * 2 // 2 bytes per int16
	bytes := make([]byte, size)
	for i, val := range ints {
		bytes[2*i] = byte(val)
		bytes[2*i+1] = byte(val >> 8)
	}
	return bytes
}
