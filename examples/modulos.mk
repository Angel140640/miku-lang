# Exemplo dos módulos novos. Instale antes:
#   miku install components time mod http economia niveis
token env("DISCORD_TOKEN")
prefix "!"

on ready:
    print(f"♪ {bot.user.name} no ar!")
    # a cada 10 minutos troca o status com a hora
    every(600, lambda: print("hora:", format_time(now(), "%H:%M", "America/Sao_Paulo")))

command limpar(n: int = 10) requires manage_messages:
    apagadas = purge(ctx.channel, n + 1)
    reply f"🧹 {apagadas - 1} mensagens apagadas"

command aviso(alvo: member, *, motivo) requires moderate_members:
    total = warn(ctx.guild_id, alvo.id, motivo, by=ctx.author.id)
    reply f"⚠️ {alvo.mention} agora tem {total} aviso(s)."

command piada:
    r = http_get("https://icanhazdadjoke.com/", headers={"Accept": "application/json"})
    if r["ok"]:
        reply r["json"]["joke"]
    else:
        reply "não consegui buscar uma piada agora"

command votar:
    reply "Gostou da Miku?", components=[
        button("Sim 💙", "voto_sim", style="success"),
        button("Muito 💙💙", "voto_muito", style="primary"),
    ]

on component(inter):
    if inter.custom_id.startswith("voto_"):
        var_add("votos", 1)
        inter.update(f"Obrigada, {inter.author.display_name}! Total de votos: {var_get('votos', 0)}", components=[])
