-- Generated from the step table in internal/isotty/upsteps.go by
-- `go test ./internal/isotty -run TestSpecSteps -update`. Do not edit.
import Spec.Model

/-- The steps of up before the restore, in order. -/
def before : List (Template Config) :=
  [ step [.vm, .hostState]
  , step [.hostState] (rerun := true)
  , step [.workspace]
  , perName .pkg .pkg (fun _ => rfl) (·.pkgs)
  , step [.nodeRepo, .pkg "nodejs"] (cond := (·.node))
  , step [] (each := fun a => [.agentCli a]) (cond := (!·.agents.isEmpty))
  , perName .tool .tool (fun _ => rfl) (·.tools)
  , perName .service .service (fun _ => rfl) (·.services)
  ]

/-- The steps of up after the restore, in order. -/
def after : List (Template Config) :=
  [ homeT
  , step [.auditArchives] (each := fun a => [.skill a .audit]) (cond := (·.archives))
  , step [.sshConfig] (rerun := true)
  , step [.sshWrappers] (rerun := true)
  , step [.auditd] (rerun := true)
  , step [.client .epweave, .keepalive, .mergeScript, .pkg "python3-tomli-w"] (each := fun a => [.skill a .epweave, .mcp a .epweave]) (cond := (fun cfg => decide (.epweave ∈ cfg.clients))) (rerun := true)
  , step [.client .github, .keepalive, .pkg "git"] (each := fun a => [.skill a .github]) (cond := (fun cfg => decide (.github ∈ cfg.clients))) (rerun := true)
  , step [.client .gitlab, .keepalive, .pkg "git"] (each := fun a => [.skill a .gitlab]) (cond := (fun cfg => decide (.gitlab ∈ cfg.clients))) (rerun := true)
  , step [.client .bitbucket, .keepalive, .pkg "git"] (each := fun a => [.skill a .bitbucket]) (cond := (fun cfg => decide (.bitbucket ∈ cfg.clients))) (rerun := true)
  , step [.client .redmine, .keepalive] (each := fun a => [.skill a .redmine]) (cond := (fun cfg => decide (.redmine ∈ cfg.clients))) (rerun := true)
  , step [.client .docker, .keepalive] (each := fun a => [.skill a .docker]) (cond := (fun cfg => decide (.docker ∈ cfg.clients))) (rerun := true)
  , step [.client .google, .keepalive] (each := fun a => [.skill a .google]) (cond := (fun cfg => decide (.google ∈ cfg.clients))) (rerun := true)
  , step [.client .aws, .keepalive] (each := fun a => [.skill a .aws]) (cond := (fun cfg => decide (.aws ∈ cfg.clients))) (rerun := true)
  , step [.client .gas, .keepalive] (each := fun a => [.skill a .gas]) (cond := (fun cfg => decide (.gas ∈ cfg.clients))) (rerun := true)
  , step [.client .dns, .keepalive] (each := fun a => [.skill a .dns]) (cond := (fun cfg => decide (.dns ∈ cfg.clients))) (rerun := true)
  , step [.client .child, .keepalive] (each := fun a => [.skill a .child]) (cond := (fun cfg => decide (.child ∈ cfg.clients))) (rerun := true)
  , step [.client .ssh, .keepalive] (each := fun a => [.skill a .ssh]) (cond := (fun cfg => decide (.ssh ∈ cfg.clients))) (rerun := true)
  , step [.pkg "tmux", .tmuxConf] (rerun := true)
  , step [.mergeScript, .pkg "python3-tomli-w"] (each := fun a => [.notifySetting a]) (cond := (!·.agents.isEmpty)) (rerun := true)
  , step [.client .idle, .mergeScript, .pkg "python3-tomli-w"] (each := fun a => [.hook a .idle]) (cond := (!·.agents.isEmpty)) (rerun := true)
  , step [.client .wake] (each := fun a => [.skill a .wake]) (cond := (!·.agents.isEmpty)) (rerun := true)
  , step [.client .open] (each := fun a => [.skill a .open]) (rerun := true)
  , opecoT
  , step [.client .sync, .mergeScript, .pkg "python3-tomli-w"] (each := fun a => [.skill a .sync, .hook a .sync]) (rerun := true)
  , step [.mutagen] (rerun := true)
  , step [.postInstall] (cond := (·.postInstall)) (rerun := true)
  ]
