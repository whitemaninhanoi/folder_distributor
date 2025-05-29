package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Destinations []string

type CopyMetric struct {
	StartTime   string  `csv:"start_time"`
	EndTime     string  `csv:"end_time"`
	Name        string  `csv:"name"`
	Destination string  `csv:"destination"`
	SizeMB      float64 `csv:"size_mb"`
	DurationSec float64 `csv:"duration_sec"`
	Success     bool    `csv:"success"`
	Mode        string  `csv:"mode"`
}

const childrenPath = "destinations.json"
const folderRegistryFile = ".distributed_folders.json"
const logFilePath = "output.log"
const setUpLogPath = "setup.log"

var (
	activityLogPath string
	progressMutex   sync.Mutex
	logMessage      func(format string, v ...any)
	setupLog        *log.Logger
	metricsMutex    sync.Mutex
	copyMetrics     []CopyMetric
)

func main() {
	watchDir := flag.String("watch", "", "Carpeta a monitorear")
	interval := flag.Int("interval", 10, "Segundos entre cada escaneo")
	parallelism := flag.Int("parallel", 1, "Copias en paralelo (1-4)")
	useTimestampName := flag.Bool("timestamp-name", false, "Usar nombre con timestamp en las copias")
	savePrompt := flag.Bool("save-prompt", false, "Guardar el prompt de la copia")
	metricsPathFlag := flag.String("metrics-path", "", "Ruta del folder donde se guardarán los logs de métricas")
	childrenPathFlag := flag.String("children-path", childrenPath, "Ruta del archivo JSON con los destinos")
	flag.Parse()

	initSetupLogger(*savePrompt)

	if *watchDir == "" {
		setupMessage("[ERROR] Tienes que indicar una carpeta a monitorear con --watch")
		return
	}

	if *parallelism < 1 || *parallelism > 4 {
		setupMessage("[ERROR] El valor de --parallel debe estar entre 1 y 4")
		return
	}

	if *metricsPathFlag != "" {
		if stat, err := os.Stat(*metricsPathFlag); err != nil || !stat.IsDir() {
			setupMessage("[ERROR] La ruta de --metrics-path no existe o no es un directorio: %s", *metricsPathFlag)
			return
		}
	}

	setupMessage("[INFO] Monitoreando: %s | Intervalo: %d segundos | Copias en paralelo: %d", *watchDir, *interval, *parallelism)
	setupMessage("[INFO] Versión: 0.2.0")

	distributedFolders := readDistributedFolders()
	for {

		cycleStart := time.Now()

		items, err := os.ReadDir(*watchDir)
		if err != nil {
			setupMessage("[ERROR] Error al escanear la carpeta: %v", err)
			time.Sleep(time.Duration(*interval) * time.Second)
			continue
		}

		for _, entry := range items {
			name := entry.Name()
			fullPath := filepath.Join(*watchDir, name)

			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}

			// Validar recibo solo si es carpeta
			if entry.IsDir() && !hasReceipt(fullPath) {
				setupMessage("[MISSING] Falta recibo de entrega para: %s", name)
				continue
			}

			currentDate := time.Now().Format("01_02_2006_15_04_05")
			promptLogDir := filepath.Join("prompt_logs", currentDate)

			dests, err := loadChildren(*childrenPathFlag)
			if err != nil {
				setupMessage("[ERROR] No se pudieron leer los destinos: %v", err)
				continue
			}

			sem := make(chan struct{}, *parallelism)
			var wg sync.WaitGroup
			var successMutex sync.Mutex
			logCreated := false

			metricsFilePath := ""

			if logMessage == nil {
				logMessage = func(format string, v ...any) {
					timestamp := time.Now().Format("2006/01/02 15:04:05")
					msg := fmt.Sprintf(format, v...)
					fmt.Printf("[%s] %s\n", timestamp, msg)
				}
			}

			var metricsPathOnce sync.Once
			for _, d := range dests {
				dest := d

				if contains(distributedFolders[name], dest) {
					setupMessage("[SKIP] Ya se entregó a %s: %s", dest, name)
					continue
				}

				sem <- struct{}{}
				wg.Add(1)

				if !logCreated && *savePrompt {
					err1 := os.MkdirAll(promptLogDir, os.ModePerm)
					if err1 != nil {
						setupMessage("[ERROR] No se pudo crear carpeta de prompt logs para %s: %v", name, err1)
						break
					}
					activityLogPath = filepath.Join(promptLogDir, fmt.Sprintf("%s_%s", currentDate, logFilePath))
					InitLogger(activityLogPath, *savePrompt)
				}

				go func(dest string) {

					defer wg.Done()
					defer func() { <-sem }()

					finalName := name
					if *useTimestampName {
						now := time.Now().Format("2006-01-02 15_04")
						base := name
						finalName = fmt.Sprintf("%s %s", now, base)
					}
					finalDest := filepath.Join(dest, finalName)
					logMessage("[COPY] Copiando a: %s", finalDest)

					var start time.Time
					var total int64 = 0

					var copied int64
					start = time.Now()
					total = calculateTotalSize(fullPath)
					err = copyDirectory(fullPath, finalDest, dest, total, &copied, start)

					originalFolderName := strings.TrimSuffix(name, ".zip")
					sourceFolderPath := filepath.Join(*watchDir, originalFolderName)
					copyReceipt(sourceFolderPath, dest)

					if err != nil {
						logMessage("[ERROR] Error al copiar a %s: %v", dest, err)
					} else {
						logMessage("[OK] Copia completa a: %s", dest)

						duration := time.Since(start).Seconds()
						sizeMB := float64(total) / (1024 * 1024)

						metricsPathOnce.Do(func() {
							baseDir := "logs"
							if *metricsPathFlag != "" {
								baseDir = filepath.Join(*metricsPathFlag, "logs")
							}

							rawHostname := extractSourceName("")
							hostname := sanitizeForFilename(rawHostname)
							buildName := sanitizeForFilename(name)
							timestamp := time.Now().Format("15-04-05")
							uniqueID := fmt.Sprintf("%06d", time.Now().UnixNano()%1e6)

							buildDir := filepath.Join(baseDir, buildName)
							metricsFilePath = filepath.Join(buildDir, fmt.Sprintf("metrics_%s_%s_%s_%s.csv", buildName, hostname, timestamp, uniqueID))
						})

						metricsMutex.Lock()
						startTime := start.UTC().Format("2006-01-02 15:04:05")
						endTime := time.Now().UTC().Format("2006-01-02 15:04:05")
						copyMetrics = append(copyMetrics, CopyMetric{
							StartTime:   startTime,
							EndTime:     endTime,
							Name:        name,
							Destination: dest,
							SizeMB:      sizeMB,
							DurationSec: duration,
							Success:     true,
							Mode:        "folder",
						})
						metricsMutex.Unlock()

						successMutex.Lock()
						deliveredList := distributedFolders[name]
						if !contains(deliveredList, dest) {
							distributedFolders[name] = append(deliveredList, dest)
						}
						successMutex.Unlock()
					}

				}(dest)
			}

			wg.Wait()

			writeDistributedFolders(distributedFolders)

			if len(copyMetrics) > 0 {
				saveMetricsCSV(metricsFilePath)
			}
			copyMetrics = nil
		}

		setupMessage("[INFO] Ciclo de monitoreo completado en %s", time.Since(cycleStart).Truncate(time.Second))
		setupMessage("[INFO] Esperando %d segundos antes del próximo escaneo...", *interval)
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func InitLogger(logPath string, savePrompt bool) func(format string, v ...any) {
	var customLogger *log.Logger

	if savePrompt {
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("\n[%s] ERROR: No se pudo abrir log: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
			os.Exit(1)
		}
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		customLogger = log.New(multiWriter, "", 0)
	} else {
		customLogger = log.New(os.Stdout, "", 0)
	}

	log.SetOutput(io.Discard)

	logMessage = func(format string, v ...any) {
		timestamp := time.Now().Format("2006/01/02 15:04:05")
		msg := fmt.Sprintf(format, v...)
		customLogger.Printf("[%s] %s", timestamp, msg)
	}
	return nil
}

func initSetupLogger(savePrompt bool) {
	var writer io.Writer
	if savePrompt {
		setupLogDir := filepath.Join("prompt_logs", "setup")
		_ = os.MkdirAll(setupLogDir, os.ModePerm)

		setupLogPath := filepath.Join(setupLogDir, setUpLogPath)

		file, err := os.OpenFile(setupLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("[FATAL] No se pudo abrir setup.log: %v\n", err)
			os.Exit(1)
		}
		writer = io.MultiWriter(os.Stdout, file)
	} else {
		writer = os.Stdout
	}
	setupLog = log.New(writer, "", 0)
}

func setupMessage(format string, v ...any) {
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	msg := fmt.Sprintf(format, v...)
	setupLog.Printf("[%s] %s", timestamp, msg)
}

func copyDirectory(src, dst, dest string, total int64, copied *int64, start time.Time) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, os.ModePerm)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		destFile, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		buf := make([]byte, 32*1024*1024) // 1MB buffer
		lastLogged := time.Now()
		var localCopied int64 = 0

		progressWriter := &ProgressWriter{
			dest:        dest,
			total:       total,
			copied:      copied,
			localCopied: &localCopied,
			start:       start,
			lastLogged:  &lastLogged,
			mutex:       &progressMutex,
		}

		_, err = io.CopyBuffer(io.MultiWriter(destFile, progressWriter), srcFile, buf)
		return err
	})
}

type ProgressWriter struct {
	dest        string
	total       int64
	copied      *int64
	localCopied *int64
	start       time.Time
	lastLogged  *time.Time
	mutex       *sync.Mutex
}

func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)

	pw.mutex.Lock()
	defer pw.mutex.Unlock()

	*pw.copied += int64(n)
	*pw.localCopied += int64(n)

	// Only log if 2s passed or >50MB copied since last log
	if time.Since(*pw.lastLogged) >= 20*time.Second || *pw.localCopied >= 50*1024*1024 {
		logProgress(pw.dest, *pw.copied, pw.total, pw.start)
		*pw.lastLogged = time.Now()
		*pw.localCopied = 0
	}

	return n, nil
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func loadChildren(path string) (Destinations, error) {
	var d Destinations
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer archivo %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("error al parsear JSON en %s: %w", path, err)
	}
	return d, nil
}

func readDistributedFolders() map[string][]string {
	data, err := os.ReadFile(folderRegistryFile)
	if err != nil {
		return make(map[string][]string)
	}
	var registry map[string][]string
	if err := json.Unmarshal(data, &registry); err != nil {
		return make(map[string][]string)
	}
	return registry
}

func writeDistributedFolders(registry map[string][]string) {
	data, _ := json.MarshalIndent(registry, "", "  ")
	tmpFile := folderRegistryFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err == nil {
		_ = os.Rename(tmpFile, folderRegistryFile)
	}
}

func hasReceipt(folderPath string) bool {
	dir := filepath.Dir(folderPath)
	base := filepath.Base(folderPath)
	pattern := filepath.Join(dir, base+" - *.txt")
	matches, _ := filepath.Glob(pattern)
	return len(matches) > 0
}

func copyReceipt(folderPath, dest string) {
	base := filepath.Base(folderPath)
	pattern := filepath.Join(filepath.Dir(folderPath), base+" - *.txt")
	matches, _ := filepath.Glob(pattern)

	for _, receiptPath := range matches {
		destPath := filepath.Join(dest, filepath.Base(receiptPath))
		err := copyFile(receiptPath, destPath)
		if err != nil {
			logMessage("Error al copiar recibo %s: %v", receiptPath, err)
		} else {
			logMessage("[OK] Recibo copiado: %s", destPath)
		}
	}
}

func calculateTotalSize(dir string) int64 {
	var total int64 = 0
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func logProgress(dest string, copied, total int64, start time.Time) {
	elapsed := time.Since(start)
	if elapsed.Seconds() < 0.001 {
		elapsed = time.Millisecond
	}

	if total == 0 {
		total = 1
	}

	percent := float64(copied) / float64(total) * 100
	if percent > 100 {
		percent = 100
		copied = total
	}

	speed := float64(copied) / elapsed.Seconds()
	eta := "<1s"
	if speed > 0 && copied < total {
		etaDuration := time.Duration(float64(total-copied)/speed) * time.Second
		eta = etaDuration.Truncate(time.Second).String()
	}

	bar := renderBar(percent, 30)
	logMessage("[COPY] [%s] %s Progreso: %.2f%% | Copiado: %.2f MB / %.2f MB | ETA: %s",
		dest,
		bar,
		percent,
		float64(copied)/(1024*1024),
		float64(total)/(1024*1024),
		eta,
	)

}

func renderBar(percent float64, width int) string {
	filled := int(percent / 100 * float64(width))
	if filled > width {
		filled = width
	}
	return fmt.Sprintf("[%s%s]", strings.Repeat("#", filled), strings.Repeat("-", width-filled))
}

func saveMetricsCSV(path string) {
	if len(copyMetrics) == 0 {
		logMessage("[SKIP] No se generaron métricas. No se guardará metrics.csv")
		return
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		logMessage("[ERROR] No se pudo crear carpeta para métricas: %v", err)
		return
	}

	file, err := os.Create(path)
	if err != nil {
		logMessage("[ERROR] No se pudo crear metrics.csv: %v", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Header
	writer.Write([]string{
		"source",
		"start_time",
		"end_time",
		"name",
		"destination",
		"size_mb",
		"size_gb",
		"duration_sec",
		"formatted_time",
		"success",
		"mode",
	})

	metricsMutex.Lock()
	copiedMetrics := make([]CopyMetric, len(copyMetrics))
	copy(copiedMetrics, copyMetrics)
	metricsMutex.Unlock()

	for _, m := range copyMetrics {
		source := extractSourceName(m.Destination)
		formattedTime := formatSeconds(m.DurationSec)
		sizeGB := m.SizeMB / 1024.0

		writer.Write([]string{
			source,
			m.StartTime,
			m.EndTime,
			m.Name,
			m.Destination,
			fmt.Sprintf("%.2f", m.SizeMB),
			fmt.Sprintf("%.2f", sizeGB),
			fmt.Sprintf("%.2f", m.DurationSec),
			formattedTime,
			fmt.Sprintf("%t", m.Success),
			m.Mode,
		})
	}
}

// Extrae el nombre del switch (hostname) desde el path
func extractSourceName(_ string) string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return "UNKNOWN"
	}
	return hostname
}

// Convierte segundos en formato HH:mm:ss
func formatSeconds(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func sanitizeForFilename(input string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		",", "_",
		":", "_",
	)
	return replacer.Replace(input)
}
