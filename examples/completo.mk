# Precisa dos módulos:  miku install shards slash embed vars
token env("DISCORD_TOKEN")
prefix "!"
intents members          # soma intents ao padrão (ligue 'Server Members Intent' no Developer Portal)
shards auto
status online
activity playing "♪ Miku Lang"

on ready:
    print(f"♪ {bot.user.name} online em {bot.guild_count} servidores")

on member_join(membro):
    canal = membro.guild.channels[0]
    canal.send(f"bem-vindo(a), {membro.mention}! ♪")

on message:
    if message.author.bot:
        return
    if "miku" in message.content.lower():
        reply "chamou? 💙"

command moedas:
    n = var_add(f"moedas:{ctx.author.id}", 1)
    reply embed(
        title="Suas moedas",
        description=f"você tem **{n}** ♪",
        fields=[("Usuário", ctx.author.display_name, True)],
        color="teal",
    )

command ban(alvo: member, *, motivo) requires ban_members:
    try:
        alvo.ban(reason=motivo)
        reply f"{alvo.name} banido."
    except as erro:
        reply f"não consegui banir: {erro}"

slash oi "Diz oi" (nome: str):
    reply f"oi, {nome}! ♪"

slash dado "Rola um dado" (lados: int = 6):
    reply f"🎲 {randint(1, lados)}", ephemeral=True
