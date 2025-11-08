package main

import (
    "archive/zip"
    "flag"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "time"
)

func main() {
    root := flag.String("root", ".", "project root path")
    outDir := flag.String("out", "archives", "output archives directory")
    dateStr := flag.String("date", time.Now().Format("20060102"), "date string for archive name, e.g. 20251108")
    dryRun := flag.Bool("dry", false, "dry run, only print actions")
    flag.Parse()

    decisionRoot := filepath.Join(*root, "decision_logs")
    fi, err := os.Stat(decisionRoot)
    if err != nil || !fi.IsDir() {
        fmt.Printf("decision_logs 目录不存在: %s\n", decisionRoot)
        os.Exit(1)
    }

    if err := os.MkdirAll(*outDir, 0o755); err != nil {
        fmt.Printf("创建输出目录失败: %v\n", err)
        os.Exit(1)
    }

    entries, err := os.ReadDir(decisionRoot)
    if err != nil {
        fmt.Printf("读取目录失败: %v\n", err)
        os.Exit(1)
    }

    for _, e := range entries {
        if !e.IsDir() { continue }
        trader := e.Name()
        traderPath := filepath.Join(decisionRoot, trader)
        outName := fmt.Sprintf("%s_%s.zip", *dateStr, sanitize(trader))
        outPath := filepath.Join(*outDir, outName)

        // collect files
        var files []string
        _ = filepath.Walk(traderPath, func(p string, info os.FileInfo, err error) error {
            if err != nil { return nil }
            if info.IsDir() { return nil }
            base := filepath.Base(p)
            if strings.HasPrefix(base, "decision_") && strings.HasSuffix(base, ".json") {
                files = append(files, p)
            }
            return nil
        })

        if len(files) == 0 {
            fmt.Printf("[跳过] %s 无可归档日志\n", trader)
            continue
        }

        if *dryRun {
            fmt.Printf("[预览] 将打包 %d 个文件到 %s\n", len(files), outPath)
            continue
        }

        if err := writeZip(outPath, traderPath, files); err != nil {
            fmt.Printf("[失败] %s: %v\n", trader, err)
            continue
        }
        fmt.Printf("[完成] %s -> %s (文件数: %d)\n", trader, outPath, len(files))
    }
}

func sanitize(name string) string {
    // 替换不适合文件名的字符
    repl := []string{" ", "_", "/", "_", "\\", "_", ":", "-"}
    for i := 0; i < len(repl); i += 2 {
        name = strings.ReplaceAll(name, repl[i], repl[i+1])
    }
    return name
}

func writeZip(outPath, baseDir string, files []string) error {
    f, err := os.Create(outPath)
    if err != nil { return err }
    defer f.Close()
    zw := zip.NewWriter(f)
    defer zw.Close()

    for _, p := range files {
        rel, err := filepath.Rel(baseDir, p)
        if err != nil { rel = filepath.Base(p) }
        w, err := zw.Create(rel)
        if err != nil { return err }
        src, err := os.Open(p)
        if err != nil { return err }
        if _, err := io.Copy(w, src); err != nil { src.Close(); return err }
        src.Close()
    }
    return nil
}