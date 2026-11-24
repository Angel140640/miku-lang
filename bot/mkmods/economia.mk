# ♪ Módulo economia — moedas por servidor (precisa dos módulos vars, embed e time)
#
# Comandos: saldo [@pessoa] · diario · trabalhar · pagar @pessoa valor · top
# Funções para usar no seu código: eco_saldo(servidor_id, usuario_id), eco_add(servidor_id, usuario_id, valor)

def _eco_gid(ctx):
    return ctx.guild_id or "dm"

def eco_saldo(gid, uid):
    return var_get(f"eco:{gid}:{uid}", 0)

def eco_add(gid, uid, valor):
    return var_add(f"eco:{gid}:{uid}", valor)

def _eco_fmt(n):
    return f"{n:,}".replace(",", ".")

# devolve 0 se já pode usar, ou os segundos que faltam
def _eco_espera(chave, intervalo):
    ultimo = var_get(chave, 0)
    falta = int(ultimo + intervalo - now())
    if falta > 0:
        return falta
    return 0

command saldo(alvo: user = None):
    pessoa = alvo or ctx.author
    gid = _eco_gid(ctx)
    reply embed(
        title="💰 Carteira",
        description=f"{pessoa.mention} tem **{_eco_fmt(eco_saldo(gid, pessoa.id))}** moedas",
        color="miku",
    )

command diario aliases daily:
    gid = _eco_gid(ctx)
    chave = f"eco:diario:{gid}:{ctx.author.id}"
    falta = _eco_espera(chave, 86400)
    if falta > 0:
        reply f"⏳ Você já pegou o prêmio diário. Volte em **{human_duration(falta)}**."
    else:
        premio = randint(200, 300)
        var_set(chave, now())
        total = eco_add(gid, ctx.author.id, premio)
        reply f"🎁 Você ganhou **{premio}** moedas! Agora tem **{_eco_fmt(total)}**."

command trabalhar aliases work:
    gid = _eco_gid(ctx)
    chave = f"eco:trabalho:{gid}:{ctx.author.id}"
    falta = _eco_espera(chave, 3600)
    if falta > 0:
        reply f"😴 Você está cansado. Descanse por **{human_duration(falta)}**."
    else:
        ganho = randint(50, 150)
        var_set(chave, now())
        total = eco_add(gid, ctx.author.id, ganho)
        reply f"💼 Você trabalhou e ganhou **{ganho}** moedas! Agora tem **{_eco_fmt(total)}**."

command pagar(alvo: user, valor: int):
    gid = _eco_gid(ctx)
    if valor <= 0:
        reply "❌ O valor precisa ser maior que zero."
    elif alvo.id == ctx.author.id:
        reply "❌ Você não pode pagar a si mesmo."
    elif alvo.bot:
        reply "❌ Bots não usam dinheiro."
    elif eco_saldo(gid, ctx.author.id) < valor:
        reply "❌ Saldo insuficiente."
    else:
        eco_add(gid, ctx.author.id, -valor)
        eco_add(gid, alvo.id, valor)
        reply f"✅ Você enviou **{_eco_fmt(valor)}** moedas para {alvo.mention}."

command top aliases ranking_moedas:
    gid = _eco_gid(ctx)
    prefixo = f"eco:{gid}:"
    itens = []
    for k in var_keys(prefixo):
        itens.append((k[len(prefixo):], var_get(k, 0)))
    itens = sorted(itens, key=lambda x: x[1], reverse=True)[:10]
    if len(itens) == 0:
        reply "Ninguém tem moedas ainda. Use `diario` para começar!"
    else:
        linhas = []
        for i, item in enumerate(itens):
            linhas.append(f"**{i + 1}.** <@{item[0]}> — {_eco_fmt(item[1])}")
        reply embed(title="🏆 Mais ricos", description="\n".join(linhas), color="yellow")
