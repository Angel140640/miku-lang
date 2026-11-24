# Miku Lang para VSCode ♪

Suporte para arquivos `.mk` da [Miku Lang](../README.md):

- realce de sintaxe (comandos, eventos, f-strings, tipos de parâmetro, funções dos módulos…)
- snippets (`bot`, `command`, `commandp`, `on`, `slash`, `embed`, `button`, `every`, …)
- comentários com `#`, indentação automática depois de `:` e dobra de blocos
- erros do `miku check` na aba **Problemas** (veja abaixo)

A extensão é só dados (JSON): não roda código nenhum e não precisa de Node.

## Instalar (sem Marketplace)

Copie esta pasta inteira para a pasta de extensões do VSCode, com o nome `mikulang.miku-lang-0.1.0`, e reabra o VSCode.

| Sistema | Pasta de extensões |
|---|---|
| Windows | `%USERPROFILE%\.vscode\extensions\` |
| macOS / Linux | `~/.vscode/extensions/` |
| Servidor (code-server, VSCodium) | `~/.vscode-server/extensions/`, `~/.vscode-oss/extensions/` |

```sh
cp -r vscode-miku ~/.vscode/extensions/mikulang.miku-lang-0.1.0
```

Abra um `.mk`: o idioma no canto inferior direito deve aparecer como **Miku**.
Se aparecer "Texto Simples", clique nele → *Configurar associação de arquivo para .mk* → **Miku**.

## Rodar e checar erros pelo VSCode

Crie `.vscode/tasks.json` na pasta do seu bot:

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "Miku: checar",
      "type": "shell",
      "command": "miku check ${file}",
      "problemMatcher": "$miku",
      "group": "build"
    },
    {
      "label": "Miku: rodar bot",
      "type": "shell",
      "command": "miku ${file}",
      "isBackground": true,
      "problemMatcher": [],
      "group": { "kind": "test", "isDefault": true }
    }
  ]
}
```

- **Ctrl+Shift+B** → "Miku: checar": os erros de sintaxe aparecem sublinhados e na aba Problemas.
- **Terminal → Executar tarefa de teste** liga o bot (o token vem do `.env` ao lado do `.mk`).

Precisa do binário `miku` no PATH (veja o README principal).

## Empacotar um `.vsix` (opcional)

Quem tem Node pode gerar o pacote: `npx @vscode/vsce package` dentro desta pasta, e depois
`code --install-extension miku-lang-0.1.0.vsix`.
