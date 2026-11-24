# Bot mínimo — só o núcleo, nenhum módulo necessário.
# Rode com:  miku ping.mk   (com DISCORD_TOKEN no ambiente ou num arquivo .env)
token env("DISCORD_TOKEN")
prefix "!"

on ready:
    print(f"♪ {bot.user.name} está no ar em {bot.guild_count} servidor(es)!")

command ping: reply "Pong! 🎤"

command oi aliases ola, hello:
    reply f"oi, {ctx.author.display_name}! ♪"

command soma(a: int, b: int):
    reply f"{a} + {b} = {a + b}"

command eco(*, texto):
    reply texto

command dado(lados: int = 6):
    reply f"🎲 {randint(1, lados)}"
