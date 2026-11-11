# Changelog pessoal: Quick Create

Trabalho de 10/11/2026. Revisão prevista para 11/11/2026, antes de iniciar a UI.
Nota descartável, fora do repositório e sem commit. Não substitui o CHANGELOG oficial.

## O que já funciona

```sh
flock app create meu-app
flock app create meu-blog --type wordpress
```

- PHP é o tipo padrão: cria uma pasta com `index.php` e uma mensagem simples.
- WordPress usa `https://wordpress.org/latest.tar.gz`, não uma versão fixa.
- O download tem timeout de 120 segundos e respeita cancelamento.
- WordPress exige o daemon e o MySQL gerenciado pelo Flock ativos antes de criar a pasta.
- O arquivo é extraído sem deixar uma subpasta `wordpress/` dentro do projeto.
- O banco recebe o nome normalizado do projeto. É criado se não existir; se existir, é reutilizado.
- A configuração usa `root`, senha vazia, `127.0.0.1` e a porta real do MySQL do Flock.
- As oito chaves e salts de autenticação são geradas localmente.
- O projeto é registrado como site, usando o TLD configurado e o PHP padrão.
- A instalação do WordPress termina no navegador; não criamos usuário administrador pela CLI.

## Proteções e limites atuais

- Não sobrescreve destinos existentes nem substitui sites ou proxies com o mesmo nome.
- PHP puro continua sem exigir banco ou daemon ativo para salvar o registro do site.
- MariaDB não é usado pelo fluxo de WordPress.
- Em falhas, os erros são exibidos e os arquivos gerados são mantidos.
- Bancos criados não são apagados automaticamente em falhas.
- Para repetir uma criação que deixou arquivos, é preciso mover ou remover a pasta primeiro.
- Ainda não há botão na interface nem endpoint de criação de aplicativos na API.

## Onde retomar

| Área | Arquivo |
| --- | --- |
| Comando, validação e escolha do tipo | [app.go](/home/felipesantos2/projects/flockphp-quick-add-project/internal/cli/app.go) |
| Download, extração, configuração e banco do WordPress | [wordpress.go](/home/felipesantos2/projects/flockphp-quick-add-project/internal/cli/wordpress.go) |
| Verificação compartilhada de banco ativo | [catalog.go](/home/felipesantos2/projects/flockphp-quick-add-project/internal/cli/catalog.go) |
| Testes do comando | [app_test.go](/home/felipesantos2/projects/flockphp-quick-add-project/internal/cli/app_test.go) |
| Testes de WordPress | [wordpress_test.go](/home/felipesantos2/projects/flockphp-quick-add-project/internal/cli/wordpress_test.go) |
| Referência de uso | [docs/cli.md](/home/felipesantos2/projects/flockphp-quick-add-project/docs/cli.md) |
| Guia básico de desenvolvimento | [CONTRIBUTING.md](/home/felipesantos2/projects/flockphp-quick-add-project/CONTRIBUTING.md) |
| Requisitos originais | [quick-app.md](/home/felipesantos2/projects/flockphp-quick-add-project/quick-app.md) |

## Validações realizadas nesta sessão

Passaram:

```sh
go test -race ./internal/cli ./internal/catalog
go test ./internal/cli -run 'Test(WordPressCLI|DownloadWordPress)' -count=1
go vet ./internal/cli ./cmd/flock
go build -o /dev/null ./cmd/flock
go run ./cmd/flock app create --help
go run ./tools/docslint
git diff --check
```

- Os testes cobrem criação PHP, nomes e destinos inválidos, conflitos, MySQL indisponível,
  download, timeout, configuração, registro do site e erros de banco.
- O fluxo integrado usa API local e cliente MySQL simulados: não foi uma instalação
  completa de WordPress com MySQL real e navegador.
- O arquivo oficial 7.1.4 foi conferido durante a implementação. Depois, a URL foi
  alterada para `latest.tar.gz`, que respondeu HTTP 200; o teste verifica essa URL.
- Links locais do guia de desenvolvimento também foram validados.
- Não executamos a suíte completa do repositório nem builds/testes das interfaces nativas.

## Revisar amanhã antes da UI

- [ ] Fazer um teste manual com MySQL real: criar WordPress e concluir a instalação no navegador.
- [ ] Conferir erros visíveis: MySQL parado, pasta existente, falha de download e banco sem acesso.
- [ ] Confirmar a UX desejada para reutilização de banco existente e arquivos mantidos em falhas.
- [ ] Escolher a primeira plataforma da UI e o local da ação "Criar aplicativo".
- [ ] Definir campos iniciais: nome, tipo e pasta de destino. Hoje a CLI usa o diretório atual.
- [ ] Extrair a lógica compartilhada que hoje está em `internal/cli` para um pacote apropriado
  antes de usá-la pelo backend/API; evitar duplicá-la nas interfaces.
- [ ] Definir o contrato da API e como acompanhar uma operação longa, seus erros e cancelamento.
- [ ] Integrar o cliente e a tela da plataforma escolhida, sem bloquear a interface durante o download.
- [ ] Manter os commits pequenos e atualizar testes/documentação conforme cada incremento.

## Commits para localizar o trabalho

| Commit | Mudança |
| --- | --- |
| `f5ffb68` | Comando inicial de criação PHP |
| `8c97458` | Testes de criação PHP e conflitos |
| `c66d9ec` | Documentação do comando inicial |
| `37c7811` | Download e extração do WordPress com timeout |
| `7b719bb` | Credenciais, chaves e salts do WordPress |
| `ec6ddf4` | Integração WordPress + MySQL ativo na CLI |
| `31b0c22` | Testes integrados e pré-requisitos |
| `6eb7209` | Documentação da criação WordPress |
| `b99ba26` | URL oficial `latest.tar.gz` |
| `35a2cfb` | Guia básico de desenvolvimento |
| `e63dbbe` | Complemento do guia: como começar uma funcionalidade |

O worktree estava limpo ao preparar esta nota.
