package analyzer

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/michalswi/local-agent-on-steroids/types"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcapgo"
	"github.com/michalswi/pdf-reader/pdf"
)

// Detector detects file metadata and content
type Detector struct{}

// NewDetector creates a new file detector
func NewDetector() *Detector {
	return &Detector{}
}

// DetectFile analyzes a file and returns its metadata
func (d *Detector) DetectFile(path string) (*types.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	ext := filepath.Ext(path)
	size := info.Size()

	fileInfo := &types.FileInfo{
		Path:      path,
		Size:      size,
		Extension: ext,
		ModTime:   info.ModTime(),
	}

	// Determine category based on size
	if size <= types.SmallFileSizeBytes {
		fileInfo.Category = types.CategorySmall
	} else if size <= types.MediumFileSizeBytes {
		fileInfo.Category = types.CategoryMedium
	} else {
		fileInfo.Category = types.CategoryLarge
	}

	// Detect file type
	fileType, err := d.detectFileType(path, ext)
	if err != nil {
		fileInfo.IsReadable = false
		fileInfo.Type = types.TypeUnknown
		return fileInfo, nil
	}

	fileInfo.Type = fileType
	fileInfo.IsReadable = (fileType == types.TypeText || fileType == types.TypePDF || fileType == types.TypePCAP)

	return fileInfo, nil
}

// detectFileType determines the type of a file
func (d *Detector) detectFileType(path, ext string) (types.FileType, error) {
	// Check by extension first
	textExts := []string{
		".txt", ".md", ".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".h",
		".rs", ".rb", ".php", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".json",
		".xml", ".html", ".css", ".sql", ".r", ".swift", ".kt", ".scala",
		// Go modules
		".mod", ".sum",
		// Infrastructure / config
		".tf", ".tfvars", ".toml", ".ini", ".cfg", ".conf", ".properties",
		// Environment / secrets (displayed but not sent to LLM)
		".env", ".key", ".pem", ".crt", ".cer",
		// Other common text formats
		".csv", ".tsv", ".lock", ".gradle", ".dockerfile", ".makefile",
	}

	for _, textExt := range textExts {
		if ext == textExt {
			return types.TypeText, nil
		}
	}

	// Files with no extension but a well-known name are text (Dockerfile, Makefile, etc.)
	base := strings.ToLower(filepath.Base(path))
	noExtTextNames := map[string]bool{
		"dockerfile": true, "makefile": true, "jenkinsfile": true,
		"gemfile": true, "brewfile": true, "procfile": true,
	}
	if ext == "" && noExtTextNames[base] {
		return types.TypeText, nil
	}

	// Check for binary types
	binaryExts := []string{
		".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a",
	}

	for _, binExt := range binaryExts {
		if ext == binExt {
			return types.TypeBinary, nil
		}
	}

	// Check for archives
	archiveExts := []string{
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
	}

	for _, archExt := range archiveExts {
		if ext == archExt {
			return types.TypeArchive, nil
		}
	}

	// Check for images
	imageExts := []string{
		".jpg", ".jpeg", ".png", ".gif", ".bmp", ".svg", ".ico", ".webp",
	}

	for _, imgExt := range imageExts {
		if ext == imgExt {
			return types.TypeImage, nil
		}
	}

	// Check for PDF files
	if ext == ".pdf" {
		return types.TypePDF, nil
	}

	// Check for PCAP files
	pcapExts := []string{
		".pcap", ".pcapng", ".cap",
	}

	for _, pcapExt := range pcapExts {
		if ext == pcapExt {
			return types.TypePCAP, nil
		}
	}

	// Try to detect by content
	file, err := os.Open(path)
	if err != nil {
		return types.TypeUnknown, err
	}
	defer file.Close()

	// Read first 512 bytes
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return types.TypeUnknown, err
	}

	// Check if content is valid UTF-8 text
	if utf8.Valid(buffer[:n]) {
		// Further validate it's text (not binary with valid UTF-8 sequences)
		textCount := 0
		for _, b := range buffer[:n] {
			if b == '\n' || b == '\r' || b == '\t' || (b >= 32 && b < 127) {
				textCount++
			}
		}

		// If more than 90% of characters are text, consider it text
		if n > 0 && float64(textCount)/float64(n) > 0.9 {
			return types.TypeText, nil
		}
	}

	return types.TypeBinary, nil
}

// ReadPDFContent extracts text from a PDF file
func (d *Detector) ReadPDFContent(path string) (string, error) {
	f, reader, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	textReader, err := reader.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("failed to extract text from PDF: %w", err)
	}

	var buf bytes.Buffer
	_, err = buf.ReadFrom(textReader)
	if err != nil {
		return "", fmt.Errorf("failed to read extracted text: %w", err)
	}

	return buf.String(), nil
}

// ReadPCAPContent extracts information from a PCAP file
func (d *Detector) ReadPCAPContent(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PCAP file: %w", err)
	}
	defer f.Close()

	reader, err := pcapgo.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("failed to create PCAP reader: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("=== PCAP File Analysis ===\n\n")

	packetSource := gopacket.NewPacketSource(reader, reader.LinkType())

	// Track statistics
	totalPackets := 0
	protocolCount := make(map[string]int)
	srcIPs := make(map[string]int)
	dstIPs := make(map[string]int)
	srcPorts := make(map[string]int)
	dstPorts := make(map[string]int)
	var firstTimestamp, lastTimestamp string

	// Sample first few packets for detailed analysis
	const maxDetailedPackets = 10
	var detailedPackets []string

	for packet := range packetSource.Packets() {
		totalPackets++

		// Capture timestamp
		if totalPackets == 1 {
			firstTimestamp = packet.Metadata().Timestamp.String()
		}
		lastTimestamp = packet.Metadata().Timestamp.String()

		// Analyze network layer
		if networkLayer := packet.NetworkLayer(); networkLayer != nil {
			if ipLayer := packet.Layer(layers.LayerTypeIPv4); ipLayer != nil {
				ip, _ := ipLayer.(*layers.IPv4)
				srcIP := ip.SrcIP.String()
				dstIP := ip.DstIP.String()
				srcIPs[srcIP]++
				dstIPs[dstIP]++
				protocolCount["IPv4"]++
			} else if ipLayer := packet.Layer(layers.LayerTypeIPv6); ipLayer != nil {
				ip, _ := ipLayer.(*layers.IPv6)
				srcIP := ip.SrcIP.String()
				dstIP := ip.DstIP.String()
				srcIPs[srcIP]++
				dstIPs[dstIP]++
				protocolCount["IPv6"]++
			}
		}

		// Analyze transport layer
		if transportLayer := packet.TransportLayer(); transportLayer != nil {
			switch transportLayer.LayerType() {
			case layers.LayerTypeTCP:
				tcp, _ := transportLayer.(*layers.TCP)
				srcPorts[fmt.Sprintf("%d", tcp.SrcPort)]++
				dstPorts[fmt.Sprintf("%d", tcp.DstPort)]++
				protocolCount["TCP"]++
			case layers.LayerTypeUDP:
				udp, _ := transportLayer.(*layers.UDP)
				srcPorts[fmt.Sprintf("%d", udp.SrcPort)]++
				dstPorts[fmt.Sprintf("%d", udp.DstPort)]++
				protocolCount["UDP"]++
			}
		}

		// Analyze application layer
		if appLayer := packet.ApplicationLayer(); appLayer != nil {
			if packet.Layer(layers.LayerTypeDNS) != nil {
				protocolCount["DNS"]++
			} else if packet.Layer(layers.LayerTypeTLS) != nil {
				protocolCount["TLS"]++
			} else if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
				// Check for HTTP on common ports
				tcp, _ := tcpLayer.(*layers.TCP)
				if tcp.DstPort == 80 || tcp.SrcPort == 80 || tcp.DstPort == 8080 || tcp.SrcPort == 8080 {
					protocolCount["HTTP"]++
				}
			}
		}

		// Capture detailed view of first few packets
		if totalPackets <= maxDetailedPackets {
			detailedPackets = append(detailedPackets, fmt.Sprintf("Packet #%d: %s", totalPackets, packet.String()))
		}

		// Limit processing for very large captures
		if totalPackets >= 100000 {
			builder.WriteString("⚠️  Large capture detected. Processing first 100,000 packets only.\n\n")
			break
		}
	}

	// Build summary
	builder.WriteString("📊 Summary:\n")
	builder.WriteString(fmt.Sprintf("- Total Packets: %d\n", totalPackets))
	builder.WriteString(fmt.Sprintf("- First Packet: %s\n", firstTimestamp))
	builder.WriteString(fmt.Sprintf("- Last Packet: %s\n\n", lastTimestamp))

	// Protocol breakdown
	builder.WriteString("📦 Protocols:\n")
	for proto, count := range protocolCount {
		percentage := float64(count) / float64(totalPackets) * 100
		builder.WriteString(fmt.Sprintf("- %s: %d packets (%.2f%%)\n", proto, count, percentage))
	}
	builder.WriteString("\n")

	// Top source IPs
	builder.WriteString("🌐 Top Source IPs:\n")
	topCount := 5
	for ip, count := range topN(srcIPs, topCount) {
		builder.WriteString(fmt.Sprintf("- %s: %d packets\n", ip, count))
	}
	builder.WriteString("\n")

	// Top destination IPs
	builder.WriteString("🎯 Top Destination IPs:\n")
	for ip, count := range topN(dstIPs, topCount) {
		builder.WriteString(fmt.Sprintf("- %s: %d packets\n", ip, count))
	}
	builder.WriteString("\n")

	// Top source ports
	builder.WriteString("🔌 Top Source Ports:\n")
	for port, count := range topN(srcPorts, topCount) {
		builder.WriteString(fmt.Sprintf("- Port %s: %d packets\n", port, count))
	}
	builder.WriteString("\n")

	// Top destination ports
	builder.WriteString("🚪 Top Destination Ports:\n")
	for port, count := range topN(dstPorts, topCount) {
		builder.WriteString(fmt.Sprintf("- Port %s: %d packets\n", port, count))
	}
	builder.WriteString("\n")

	// Sample packets
	if len(detailedPackets) > 0 {
		builder.WriteString("📋 Sample Packets (first 10):\n")
		for i, pkt := range detailedPackets {
			if i >= 3 { // Only show first 3 in detail to keep it concise
				break
			}
			builder.WriteString(fmt.Sprintf("\n%s\n", pkt))
		}
	}

	return builder.String(), nil
}

// topN returns the top N items from a map by value
func topN(m map[string]int, n int) map[string]int {
	type kv struct {
		key   string
		value int
	}

	var sorted []kv
	for k, v := range m {
		sorted = append(sorted, kv{k, v})
	}

	// Simple bubble sort for top N
	for i := 0; i < len(sorted) && i < n; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].value > sorted[i].value {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	result := make(map[string]int)
	for i := 0; i < len(sorted) && i < n; i++ {
		result[sorted[i].key] = sorted[i].value
	}

	return result
}

// ReadContent reads the content of a file
func (d *Detector) ReadContent(path string, maxLines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // allow large lines
	lineCount := 0

	for scanner.Scan() {
		if maxLines > 0 && lineCount >= maxLines {
			break
		}

		if lineCount > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(scanner.Text())
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to scan file: %w", err)
	}

	return builder.String(), nil
}

// CountLines counts the number of lines in a file
func (d *Detector) CountLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	lineCount := 0

	for scanner.Scan() {
		lineCount++
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to scan file: %w", err)
	}

	return lineCount, nil
}

// IsBinary checks if a file appears to be binary
func (d *Detector) IsBinary(path string) (bool, error) {
	fileType, err := d.detectFileType(path, filepath.Ext(path))
	if err != nil {
		return true, err
	}

	return fileType == types.TypeBinary, nil
}

// GetMimeType attempts to determine MIME type of a file
func (d *Detector) GetMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	mimeTypes := map[string]string{
		".txt":    "text/plain",
		".md":     "text/markdown",
		".go":     "text/x-go",
		".py":     "text/x-python",
		".js":     "text/javascript",
		".json":   "application/json",
		".xml":    "application/xml",
		".yaml":   "application/yaml",
		".yml":    "application/yaml",
		".html":   "text/html",
		".css":    "text/css",
		".jpg":    "image/jpeg",
		".png":    "image/png",
		".gif":    "image/gif",
		".pdf":    "application/pdf",
		".zip":    "application/zip",
		".pcap":   "application/vnd.tcpdump.pcap",
		".pcapng": "application/x-pcapng",
		".cap":    "application/vnd.tcpdump.pcap",
	}

	if mime, ok := mimeTypes[ext]; ok {
		return mime
	}

	return "application/octet-stream"
}
