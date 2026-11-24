# ♪ Módulo niveis — XP por mensagem, níveis e ranking por servidor (precisa dos módulos vars, embed e time)
#
# Comandos: rank [@pessoa] · ranking
# Ganha 15–25 XP por mensagem, no máximo 1 vez por minuto. Nível = raiz de (XP / 100).

def nivel_de(xp):
    return int((xp / 100) ** 0.5)

def xp_do_nivel(n):
    return n * n * 100

def xp_de(gid, uid):
    return var_get(f"lvl:xp:{gid}:{uid}", 0)

on message(message):
    if message.author.bot or not message.guild_id:
        return
    gid = message.guild_id
    uid = message.author.id
    chave = f"lvl:cd:{gid}:{uid}"
    if now() - var_get(chave, 0) < 60:
        return
    var_set(chave, now())
    antes = xp_de(gid, uid)
    depois = var_add(f"lvl:xp:{gid}:{uid}", randint(15, 25))
    if nivel_de(depois) > nivel_de(antes):
        reply f"🎉 {message.author.mention} subiu para o **nível {nivel_de(depois)}**!"

command rank(alvo: user = None):
    pessoa = alvo or ctx.author
    if not ctx.guild_id:
        reply "Esse comando só funciona em servidores."
        return
    xp = xp_de(ctx.guild_id, pessoa.id)
    n = nivel_de(xp)
    proximo = xp_do_nivel(n + 1)
    reply embed(
        title=f"📈 {pessoa.display_name}",
        fields=[("Nível", str(n), True), ("XP", f"{xp}/{proximo}", True)],
        color="miku",
    )

command ranking aliases leaderboard:
    if not ctx.guild_id:
        reply "Esse comando só funciona em servidores."
        return
    prefixo = f"lvl:xp:{ctx.guild_id}:"
    itens = []
    for k in var_keys(prefixo):
        itens.append((k[len(prefixo):], var_get(k, 0)))
    itens = sorted(itens, key=lambda x: x[1], reverse=True)[:10]
    if len(itens) == 0:
        reply "Ninguém tem XP ainda. Converse no chat!"
    else:
        linhas = []
        for i, item in enumerate(itens):
            linhas.append(f"**{i + 1}.** <@{item[0]}> — nível {nivel_de(item[1])} ({item[1]} XP)")
        reply embed(title="🏆 Ranking de XP", description="\n".join(linhas), color="purple")
