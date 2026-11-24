# Miku Lang ♪

A linguagem de programação da Hatsune Miku para bots de Discord. Sintaxe parecida com Python, com comandos
prontos no estilo BDFD (`command`, `on`, `slash`, `reply`).

**É um programa único.** Não precisa de Python, Node nem nada instalado: baixou o binário, funciona.

## Instalar

Baixe o binário da sua plataforma (pasta `dist/`):

| Sistema | Arquivo |
|---|---|
| Android (Termux), Raspberry Pi, Linux ARM | `miku-linux-arm64` |
| Linux x86-64 (VPS, PC) | `miku-linux-amd64` |
| Windows | `miku-windows-amd64.exe` |
| macOS Apple Silicon / Intel | `miku-darwin-arm64` / `miku-darwin-amd64` |

```sh
chmod +x miku-linux-arm64
mv miku-linux-arm64 ~/../usr/bin/miku     # no Termux; em Linux: sudo mv ... /usr/local/bin/miku
miku help
```

## Primeiro bot

```sh
miku new app.mk                 # cria um bot de exemplo
echo 'DISCORD_TOKEN=seu_token' > .env
miku app.mk
```

```
token env("DISCORD_TOKEN")
prefix "!"

on ready:
    print(f"♪ {bot.user.name} está no ar!")

command ping:
    reply "Pong! 🎤"
```

## Comandos

```
miku help                 ajuda
miku <arquivo.mk>         roda o bot (o mesmo que: miku run arquivo.mk)
miku check <arquivo.mk>   só confere a sintaxe
miku new <arquivo.mk>     cria um bot de exemplo
miku install <módulo>     instala um módulo (embutido, arquivo .mk ou URL)
miku uninstall <módulo>   remove
miku list                 lista os módulos
miku version
```

O token é lido de `DISCORD_TOKEN` (variável de ambiente ou arquivo `.env` na pasta atual / do `.mk`).
Ative **Message Content Intent** em *Developer Portal → Bot* para os comandos com prefixo funcionarem.

## A linguagem

Variáveis, números, textos, listas, dicionários, tuplas, `if/elif/else`, `while`, `for`, `def`, `lambda`,
compreensão de lista (`[x*2 for x in xs if x > 1]`), `try/except/finally`, `raise`, f-strings com formato
(`f"{n:,.2f}"`), fatias (`x[1:3]`), `global`. `await` é aceito e ignorado (não precisa de `async`).
Não existem: classes, `import`, conjuntos (`set`), geradores.

Funções embutidas: `print len str int float bool list tuple dict range min max sum abs round sorted reversed
enumerate zip any all type ord chr divmod random randint choice shuffle sleep now env to_json from_json`.

Métodos de texto (`upper lower title split join replace strip startswith find format ...`), lista
(`append pop insert remove sort ...`) e dicionário (`get keys values items pop update ...`) como no Python.

## Diretivas do bot

```
token env("DISCORD_TOKEN")
prefix "!"
intents members, presences        # soma ao padrão; "all" liga todas
status dnd                        # online | idle | dnd | invisible
activity playing "texto"          # playing | listening | watching | competing
shards auto                       # módulo shards (ou: shards 4)
```

### `command`

```
command soma(a: int, b: int = 10):        # argumentos tipados, com padrão
    reply f"{a + b}"

command oi aliases ola, hello:            # apelidos
    reply f"oi, {ctx.author.display_name}!"

command eco(*, texto):                    # *, nome  = pega o resto da linha
    reply texto

command quem(alvo: user): ...             # tipos: str int float bool user channel role
command todos(*nomes): ...                # *nome = lista com o resto
```

Se o usuário errar um argumento, o bot já responde com o uso (`Uso: !soma <a> [b]`).
Dentro do comando existe `ctx` (e também `message`).

### `reply` / `send`

```
reply "texto"                        # responde citando a mensagem
reply "a", "b"                       # várias linhas
reply embed(title="Oi")              # embed (módulo embed)
reply "x", embed=embed(title="T")
send "texto sem citar"
```

`@everyone`, `@here` e cargos **não** são marcados por padrão (segurança).

### `on` — eventos

| Evento | Parâmetros |
|---|---|
| `ready` | — (roda uma vez) |
| `message` | `message` (todas, inclusive de bots) |
| `message_edit`, `message_delete` | `message` (parcial: só o que o Discord manda) |
| `member_join`, `member_update` | `member` |
| `member_remove` | `user`, `guild` |
| `reaction_add`, `reaction_remove` | `reaction` |
| `guild_join`, `guild_remove` | `guild` |
| `voice_state_update` | `state` (dicionário cru) |

Renomeie os parâmetros se quiser: `on member_join(membro):`. Qualquer outro evento do gateway chega cru:
`on channel_create(data):` recebe o dicionário do Discord.

### `slash` (módulo `slash`)

```
slash dado "Rola um dado" (lados: int = 6):
    reply f"🎲 {randint(1, lados)}", ephemeral=True
```

Registrados sozinhos ao ligar. Para aparecerem na hora num servidor de teste, defina `MIKU_GUILD=id_do_servidor`.
Se o código demorar mais de ~2,5 s a Miku já avisa o Discord que está "pensando"; `ctx.defer()` faz isso à mão.

## Objetos

- **mensagem/ctx**: `id content author member channel guild mentions attachments jump_url` · `reply() send() delete() react("👍") edit()`
- **user**: `id name display_name username mention bot avatar_url` · `send()` (DM)
- **member**: tudo do user + `nick roles guild joined_at` · `kick(reason=) ban(reason=) add_role(id) remove_role(id) timeout(segundos)`
- **channel**: `id name mention guild_id` · `send() typing()`
- **guild**: `id name member_count owner_id icon_url channels` · `get_member(id) leave()`
- **bot** (global): `user guilds guild_count prefix shard_count` · `send(canal_id, ...) get_user(id) get_channel(id) get_guild(id)`

## Módulos (`miku install <nome>`)

O núcleo só traz o necessário para ligar um bot. O resto você instala quando precisa:

| Módulo | O que dá |
|---|---|
| `shards` | diretiva `shards auto` / `shards 4` (bots grandes) |
| `slash` | diretiva `slash` |
| `embed` | `embed(title=, description=, color=, fields=, footer=, image=, thumbnail=, author=, url=, timestamp=)` |
| `vars` | `var_get var_set var_add var_has var_del` — guardados em `miku_vars.json` |
| `rest` | `rest_request("GET", "/users/@me")`, `rest_send(canal, ...)`, `rest_user(id)` |

Se você usar algo de um módulo que não instalou, o erro já diz qual `miku install` rodar.

**Módulos seus**: qualquer arquivo `.mk` vira módulo — `miku install ./util.mk` (ou uma URL). Ele roda antes do
seu programa, então as funções (`def`) e até `command`s dele ficam disponíveis. Módulos de URL rodam com as
mesmas permissões do bot: instale só de fontes confiáveis. Os dados ficam em `~/.miku` (ou `$MIKU_HOME`).

## Limitações atuais

Sem voz, sem botões/menus/modais (dá para tratar `on interaction_create(data)` cru), sem envio de arquivos,
sem cache de mensagens antigas.

## Compilar do código-fonte

Precisa só do Go (1.22+): `go build -o miku .` — ou `./build.sh` para todas as plataformas.
Só usa a biblioteca padrão do Go (nenhuma dependência externa).
