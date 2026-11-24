// Miku Lang — a linguagem de bots da Hatsune Miku. Um binário só, sem Python nem Node.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"miku/bot"
	"miku/discord"
	"miku/lang"
)

const version = "0.2.0"

const helpText = `Miku Lang v` + version + ` — a linguagem de bots da Hatsune Miku ♪
(um programa só: não precisa de Python, Node nem nada instalado)

uso:
  miku help                 mostra esta ajuda
  miku <arquivo.mk>         roda o bot
  miku run <arquivo.mk>     o mesmo que acima
  miku check <arquivo.mk>   só confere a sintaxe
  miku new <arquivo.mk>     cria um bot de exemplo
  miku install <módulo>     instala um módulo (ou um .mk seu / uma URL)
  miku uninstall <módulo>   remove um módulo
  miku list                 lista os módulos
  miku version              mostra a versão

o núcleo traz só o necessário para ligar um bot (token, prefix, command, on).
o resto vem por módulo:  miku install shards | slash | embed | vars | rest

o token vem de DISCORD_TOKEN (ambiente ou arquivo .env).`

const starter = `token env("DISCORD_TOKEN")
prefix "!"

on ready:
    print(f"♪ {bot.user.name} está no ar!")

command ping:
    reply "Pong! 🎤"

command oi aliases ola:
    reply f"oi, {ctx.author.display_name}! ♪"
`

var useColor = func() bool {
	if os.Getenv("NO_COLOR") != "" || runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" {
		return false
	}
	st, err := os.Stdout.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}()

func paint(code, s string) string {
	if !useColor {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func teal(s string) string { return paint("38;2;57;197;187", s) }
func pink(s string) string { return paint("38;2;226;83;154", s) }

func fail(format string, a ...any) { fmt.Fprintln(os.Stderr, pink("✗ ")+fmt.Sprintf(format, a...)) }
func ok(format string, a ...any)   { fmt.Println(teal("✓ ") + fmt.Sprintf(format, a...)) }

func logf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "[miku] "+format+"\n", a...)
}

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	platformFix()
	if len(args) == 0 {
		fmt.Println(helpText)
		return 0
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Println(helpText)
		return 0
	case "version", "-v", "--version":
		fmt.Println("miku " + version)
		return 0
	case "run":
		return cmdRun(rest)
	case "check":
		return cmdCheck(rest)
	case "new":
		return cmdNew(rest)
	case "install":
		return cmdInstall(rest)
	case "uninstall":
		return cmdUninstall(rest)
	case "list":
		return cmdList()
	}
	if strings.HasSuffix(cmd, ".mk") {
		return cmdRun(args)
	}
	if st, err := os.Stat(cmd); err == nil && !st.IsDir() {
		return cmdRun(args)
	}
	fail("comando desconhecido: %s — rode: miku help", cmd)
	return 2
}

func readSource(args []string) (path, src string, code int) {
	if len(args) == 0 {
		fail("faltou o arquivo .mk — exemplo: miku app.mk")
		return "", "", 2
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		fail("não consegui abrir %s: %v", args[0], err)
		return "", "", 1
	}
	return args[0], string(raw), 0
}

func printLangError(path, src string, err error) {
	line, msg := lang.ErrorLine(err), lang.ErrorMessage(err)
	if line > 0 {
		fail("%s:%d: %s", path, line, msg)
		lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
		if line <= len(lines) {
			fmt.Fprintf(os.Stderr, "    %d | %s\n", line, strings.TrimRight(lines[line-1], " \t"))
		}
		return
	}
	fail("%s: %s", path, msg)
}

func loadEnvFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); !exists && key != "" {
			os.Setenv(key, value)
		}
	}
}

func cmdRun(args []string) int {
	path, src, code := readSource(args)
	if code != 0 {
		return code
	}
	prog, err := lang.Parse(src)
	if err != nil {
		printLangError(path, src, err)
		return 1
	}
	loadEnvFile(".env")
	if abs, err := filepath.Abs(path); err == nil {
		loadEnvFile(filepath.Join(filepath.Dir(abs), ".env"))
	}
	if v := os.Getenv("MIKU_API_BASE"); v != "" {
		discord.APIBase = v
	}
	if v := os.Getenv("MIKU_GATEWAY_URL"); v != "" {
		discord.GatewayURL = v
	}

	in := lang.New(os.Stdout)
	b := bot.New(in, bot.InstalledSet(), logf)

	for _, mp := range bot.CustomPaths() {
		raw, err := os.ReadFile(mp)
		if err != nil {
			fail("não consegui ler o módulo %s: %v", mp, err)
			return 1
		}
		mprog, err := lang.Parse(string(raw))
		if err == nil {
			err = in.Exec(mprog)
		}
		if err != nil {
			printLangError(mp, string(raw), err)
			return 1
		}
	}
	if err := in.Exec(prog); err != nil {
		printLangError(path, src, err)
		return 1
	}
	if !b.HasHandlers() {
		return 0 // era só um script: nada para ouvir no Discord
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := b.Run(ctx); err != nil {
		fail("%v", err)
		return 1
	}
	return 0
}

func cmdCheck(args []string) int {
	path, src, code := readSource(args)
	if code != 0 {
		return code
	}
	if _, err := lang.Parse(src); err != nil {
		printLangError(path, src, err)
		return 1
	}
	ok("%s: sintaxe ok", path)
	return 0
}

func cmdNew(args []string) int {
	if len(args) == 0 {
		fail("faltou o nome do arquivo — exemplo: miku new app.mk")
		return 2
	}
	if _, err := os.Stat(args[0]); err == nil {
		fail("%s já existe, não vou sobrescrever", args[0])
		return 1
	}
	if err := os.WriteFile(args[0], []byte(starter), 0o644); err != nil {
		fail("%v", err)
		return 1
	}
	ok("%s criado — defina DISCORD_TOKEN (ou um arquivo .env) e rode: miku %s", args[0], args[0])
	return 0
}

func cmdInstall(args []string) int {
	if len(args) == 0 {
		fail("uso: miku install <módulo>   (veja: miku list)")
		return 2
	}
	code := 0
	for _, target := range args {
		msg, err := bot.Install(target)
		if err != nil {
			fail("%v", err)
			code = 1
			continue
		}
		ok("%s", msg)
	}
	return code
}

func cmdUninstall(args []string) int {
	if len(args) == 0 {
		fail("uso: miku uninstall <módulo>")
		return 2
	}
	code := 0
	for _, name := range args {
		msg, err := bot.Uninstall(name)
		if err != nil {
			fail("%v", err)
			code = 1
			continue
		}
		ok("%s", msg)
	}
	return code
}

func cmdList() int {
	for _, m := range bot.List() {
		mark := "○ disponível"
		if m.Installed {
			mark = teal("● instalado ")
		}
		fmt.Printf("  %s  %-8s %s\n", mark, m.Name, m.Desc)
	}
	return 0
}
