package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	logBase         = "/var/log/pods"
	backupPath      = "/backup"
	patternList     = []string{}
	removeAfterCopy = false
)

const version = "1.0.0"

func main() {
	printHeader()

	if env := os.Getenv("BACKUP_PATTERN"); env != "" {
		patternList = strings.Split(env, ",")
		logInfo("🧭 BACKUP_PATTERN definido: %v", patternList)
	} else {
		logWarn("🔄 BACKUP_PATTERN não definido. Usando '*'")
		patternList = []string{"*"}
	}

	if env := os.Getenv("REMOVE_AFTER_COPY"); env == "1" || strings.ToLower(env) == "true" {
		removeAfterCopy = true
		logInfo("🗑️  REMOVE_AFTER_COPY ativado. Logs serão removidos após o backup.")
	} else {
		logInfo("📁 REMOVE_AFTER_COPY desativado. Logs serão preservados.")
	}

	logInfo("🚀 Iniciando rotina...")

	config, err := rest.InClusterConfig()
	if err != nil {
		logFatal("❌ Erro ao obter configuração in-cluster: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logFatal("❌ Erro ao criar clientset do Kubernetes: %v", err)
	}

	go periodicArchive()

	watchPodDeletions(clientset)
}

func periodicArchive() {
	for {
		logInfo("⏱️ Iniciando varredura periódica de logs antigos (*.gz)...")
		for _, pattern := range patternList {
			globPath := filepath.Join(logBase, pattern+"_*")
			matches, err := filepath.Glob(globPath)
			if err != nil {
				logError("❌ Erro ao fazer globbing em %s: %v", globPath, err)
				continue
			}
			for _, podDir := range matches {
				ns, pod := parsePodDir(podDir)
				err := filepath.Walk(podDir, func(path string, info os.FileInfo, err error) error {
					if err != nil || info.IsDir() {
						return nil
					}
					if strings.HasSuffix(path, ".gz") && fileOlderThan(path, 60) {
						destDir := filepath.Join(backupPath, ns, pod)
						os.MkdirAll(destDir, 0755)

						dst := filepath.Join(destDir, generateUniqueFilename(path))
						if !fileExists(dst) {
							logInfo("📦 Arquivando log antigo: %s → %s", path, dst)
							copyFile(path, dst)
							if removeAfterCopy {
								os.Remove(path)
								logInfo("🗑️ Removido original: %s", path)
							}
						}
					}
					return nil
				})
				if err != nil {
					logError("❌ Erro ao percorrer %s: %v", podDir, err)
				}
			}
		}
		time.Sleep(1 * time.Minute)
	}
}

func watchPodDeletions(clientset *kubernetes.Clientset) {
	logInfo("🔎 Iniciando watch para eventos de pods...")

	watcher, err := clientset.CoreV1().Pods("").Watch(context.TODO(), meta.ListOptions{
		FieldSelector: fields.Everything().String(),
		Watch:         true,
	})
	if err != nil {
		logFatal("❌ Erro ao iniciar watcher de pods: %v", err)
	}

	for event := range watcher.ResultChan() {
		logInfo("📡 Evento recebido: %v", event.Type)

		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			logWarn("⚠️  Objeto recebido não é um *v1.Pod: %T", event.Object)
			continue
		}

		logInfo("🔍 Pod: %s/%s | Phase: %s | DeletionTimestamp: %v", pod.Namespace, pod.Name, pod.Status.Phase, pod.DeletionTimestamp)

		if event.Type == watch.Deleted || pod.ObjectMeta.DeletionTimestamp != nil {
			if shouldArchive(pod.Namespace) {
				logInfo("📦 Arquivando logs do pod finalizado: %s/%s", pod.Namespace, pod.Name)
				go archivePodLogs(pod.Namespace, pod.Name)
			}
		}
	}
}

func archivePodLogs(namespace, podName string) {
	globPattern := filepath.Join(logBase, fmt.Sprintf("%s_%s_*", namespace, podName))
	matches, err := filepath.Glob(globPattern)
	if err != nil || len(matches) == 0 {
		logWarn("📁 Nenhum diretório encontrado para pod %s/%s", namespace, podName)
		return
	}

	for _, podDir := range matches {
		logInfo("📁 Verificando diretório: %s", podDir)
		err := filepath.Walk(podDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".gz") || strings.HasSuffix(path, ".log") {
				destDir := filepath.Join(backupPath, namespace, podName)
				os.MkdirAll(destDir, 0755)

				dst := filepath.Join(destDir, generateUniqueFilename(path))
				if !fileExists(dst) {
					logInfo("📦 Copiando %s → %s", path, dst)
					copyFile(path, dst)
					if removeAfterCopy {
						os.Remove(path)
						logInfo("🗑️ Deletado original: %s", path)
					}
				} else {
					logInfo("⚠️  Arquivo já existente, ignorado: %s", dst)
				}
			}
			return nil
		})
		if err != nil {
			logError("❌ Erro ao processar %s: %v", podDir, err)
		}
	}
}

func generateUniqueFilename(src string) string {
	base := filepath.Base(src)
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s", timestamp, base)
}

func fileOlderThan(path string, ageSec int) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > time.Duration(ageSec)*time.Second
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) {
	from, err := os.Open(src)
	if err != nil {
		logError("❌ Erro ao abrir arquivo %s: %v", src, err)
		return
	}
	defer from.Close()

	to, err := os.Create(dst)
	if err != nil {
		logError("❌ Erro ao criar destino %s: %v", dst, err)
		return
	}
	defer to.Close()

	_, err = io.Copy(to, from)
	if err != nil {
		logError("❌ Erro ao copiar conteúdo de %s: %v", src, err)
		return
	}

	info, err := os.Stat(src)
	if err == nil {
		os.Chmod(dst, info.Mode())
		os.Chtimes(dst, time.Now(), info.ModTime())
	}
}

func parsePodDir(podDir string) (namespace, pod string) {
	parts := strings.Split(filepath.Base(podDir), "_")
	if len(parts) >= 2 {
		return parts[0], parts[1]
	}
	return "unknown", "unknown"
}

func shouldArchive(ns string) bool {
	if len(patternList) == 1 && patternList[0] == "*" {
		return true
	}
	for _, pattern := range patternList {
		p := strings.TrimRight(pattern, "*")
		if strings.HasPrefix(ns, p) {
			return true
		}
	}
	return false
}

func printHeader() {
	fmt.Printf("🟢 k8slogbck - Backup de Logs de Pods v%s\n", version)
	fmt.Println("📦 Diretório de logs:", logBase)
	fmt.Println("💾 Diretório de backup:", backupPath)
	fmt.Println()
}

func logInfo(format string, args ...any) {
	fmt.Printf(time.Now().Format("2006-01-02 15:04:05")+" ℹ️  "+format+"\n", args...)
}

func logWarn(format string, args ...any) {
	fmt.Printf(time.Now().Format("2006-01-02 15:04:05")+" ⚠️  "+format+"\n", args...)
}

func logError(format string, args ...any) {
	fmt.Printf("%s ❌ "+format+"\n", append([]any{time.Now().Format("2006-01-02 15:04:05")}, args...)...)
}

func logFatal(format string, args ...any) {
	logError(format, args...)
	os.Exit(1)
}
