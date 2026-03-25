package main

import (
	"flag"
	"fmt"
	"log"
)

func main() {
	// Parse command line flags
	port := flag.Int("port", 8080, "Port to listen on")
	flag.Parse()

	// Setup and start server
	r := SetupServer(*port)

	// Start server
	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Starting Web SSH server on %s", addr)
	log.Println("Open http://localhost" + addr + " in your browser")

	if err := r.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}