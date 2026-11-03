// Package settings lists everything an administrator can configure, with its
// default, its checks and where its value comes from: environment variable
// (locked), console (saved in the database), or default.
package settings

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Kind is the type of a setting.
type Kind string

const (
	Text   Kind = "text"
	Number Kind = "number"
	Toggle Kind = "toggle"
	Choice Kind = "choice"
	List   Kind = "list"
)

// Def describes a setting.
type Def struct {
	Key     string
	Group   string
	Label   string
	Help    string
	Kind    Kind
	Default string
	Options []string
	Min     int
	Max     int
	// Env fixes the value; the console then shows it read-only.
	Env string
	// Active is false until the feature that reads the setting exists.
	Active bool
	// Restart: the new value applies when the service restarts.
	Restart bool
}

// Group is a section of the configuration page.
type Group struct {
	ID    string
	Label string
}

var Groups = []Group{
	{"general", "Général"},
	{"rendering", "Moteur de rendu"},
	{"documents", "Documents produits"},
	{"fast", "Moteur rapide ⚡"},
	{"native", "Moteur natif (Typst)"},
	{"async", "Rendu asynchrone"},
	{"storage", "Stockage des fichiers"},
	{"history", "Historique"},
	{"security", "Sécurité"},
	{"logs", "Journaux"},
	{"mail", "E-mails des alertes"},
}

// Defs is every console setting, in display order.
var Defs = []Def{
	{Key: "instance_name", Group: "general", Label: "Nom de l'instance", Help: "Affiché dans l'en-tête de la console : INT, REC, PPROD, PROD.", Kind: Text, Max: 20, Env: "INSTANCE_NAME", Active: true},
	{Key: "public_url", Group: "general", Label: "Adresse publique", Help: "Adresse HTTPS de l'instance, utilisée dans les liens et pour sécuriser les cookies.", Kind: Text, Max: 200, Env: "PUBLIC_URL", Active: true},

	{Key: "render_workers", Group: "rendering", Label: "Workers LibreOffice", Help: "Documents produits en parallèle. Chaque worker utilise environ 300 Mo de mémoire. 0 : un par vCPU de la machine ou de la tâche, 4 au plus.", Kind: Number, Default: "0", Min: 0, Max: 8, Env: "RENDER_WORKERS", Active: true, Restart: true},
	{Key: "render_timeout_seconds", Group: "rendering", Label: "Délai maximal d'un rendu (secondes)", Help: "Au-delà, le rendu est arrêté et le worker relancé. Doit rester sous le délai du proxy ou du CDN placé devant DocIbis (souvent 60 secondes).", Kind: Number, Default: "45", Min: 5, Max: 55, Active: true, Restart: true},
	{Key: "render_queue_size", Group: "rendering", Label: "Demandes en attente", Help: "Au-delà, DocIbis répond « occupé » (503) avec un délai pour réessayer.", Kind: Number, Default: "20", Min: 0, Max: 1000, Active: true, Restart: true},
	{Key: "worker_recycle_after", Group: "rendering", Label: "Recycler un worker après (rendus)", Help: "Relance préventive d'un worker pour libérer sa mémoire.", Kind: Number, Default: "500", Min: 10, Max: 100000, Active: true, Restart: true},
	{Key: "max_template_size_mb", Group: "rendering", Label: "Taille maximale d'un modèle (Mo)", Kind: Number, Default: "20", Min: 1, Max: 200, Active: true},
	{Key: "max_data_size_mb", Group: "rendering", Label: "Taille maximale des données JSON (Mo)", Kind: Number, Default: "5", Min: 1, Max: 100, Active: true},

	{Key: "default_output", Group: "documents", Label: "Format par défaut", Kind: Choice, Default: "pdf", Options: []string{"pdf", "docx"}},
	{Key: "default_pdfa", Group: "documents", Label: "PDF/A par défaut", Help: "Norme d'archivage appliquée quand la demande n'en précise pas.", Kind: Choice, Default: "none", Options: []string{"none", "PDF/A-1b", "PDF/A-2b", "PDF/A-3b"}, Active: true},
	{Key: "default_accessible", Group: "documents", Label: "PDF accessibles par défaut", Help: "PDF balisés, conformes PDF/UA-1 (vérifiés avec veraPDF) : lisibles par les lecteurs d'écran. Une demande peut le choisir elle-même. Avec PDF/A, préférez PDF/A-2b ou 3b : PDF/A-1b reste balisé mais sans la marque PDF/UA.", Kind: Toggle, Default: "false", Active: true},
	{Key: "watermark_text", Group: "documents", Label: "Filigrane", Help: "Texte ajouté sur chaque page des PDF quand la demande n'en précise pas, par exemple SPÉCIMEN hors production. Vide : aucun. Il ne se combine pas avec PDF/A ni avec les PDF accessibles : ceux-ci l'emportent.", Kind: Text, Max: 100, Active: true},
	{Key: "watermark_color", Group: "documents", Label: "Couleur du filigrane", Help: "Au format #RRGGBB.", Kind: Text, Default: "#C0C0C0", Max: 7, Active: true},
	{Key: "watermark_size", Group: "documents", Label: "Taille du filigrane (points)", Help: "0 : adaptée à la page.", Kind: Number, Default: "0", Min: 0, Max: 300, Active: true},
	{Key: "watermark_angle", Group: "documents", Label: "Angle du filigrane (degrés)", Kind: Number, Default: "45", Min: 0, Max: 359, Active: true},
	{Key: "watermark_tiled", Group: "documents", Label: "Répéter le filigrane sur toute la page", Kind: Toggle, Default: "false", Active: true},
	{Key: "reference_threshold_permille", Group: "documents", Label: "Écart toléré avec le PDF de référence (pour mille)", Help: "Un modèle est conforme si chaque page diffère de son rendu Docmosis de moins de ce seuil.", Kind: Number, Default: "3", Min: 0, Max: 100, Active: true},
	{Key: "image_url_allowlist", Group: "documents", Label: "Domaines autorisés pour les images", Help: "Un domaine par ligne, sous-domaines compris (par exemple cdn.exemple.fr). Les images données par leur adresse dans les données (https://…) ne viennent que de ces domaines ; vide : ces images sont laissées de côté.", Kind: List, Max: 2000, Active: true},
	{Key: "image_cache_minutes", Group: "documents", Label: "Cache des images (minutes)", Help: "Une image lue par son adresse est gardée ce temps en mémoire. 0 : relue à chaque document.", Kind: Number, Default: "60", Min: 0, Max: 10080, Active: true},

	{Key: "fast_engine", Group: "fast", Label: "Utiliser le moteur rapide", Help: "Les modèles à mise en page fixe, qualifiés au pixel près, sont remplis par un calque posé sur leur fond déjà rendu ; les autres passent par LibreOffice.", Kind: Toggle, Default: "true", Active: true},
	{Key: "fast_engine_pixel_threshold", Group: "fast", Label: "Écart pixel toléré (pour mille)", Help: "Un modèle est qualifié si ses rendus par le calque diffèrent de ceux de LibreOffice de moins de ce seuil.", Kind: Number, Default: "1", Min: 0, Max: 50, Active: true},

	{Key: "native_engine", Group: "native", Label: "Utiliser le moteur natif", Help: "Un modèle qui a une copie Typst, écrite à la main pour sa version et identique au pixel près à LibreOffice, est produit sans LibreOffice, environ 30 fois plus vite. Une nouvelle version du modèle Word repasse par LibreOffice jusqu'à ce qu'une nouvelle copie soit jointe et vérifiée. Désactivé : LibreOffice produit tout.", Kind: Toggle, Default: "true", Active: true},
	{Key: "native_engine_pixel_threshold", Group: "native", Label: "Écart pixel toléré (pour mille)", Help: "Une copie Typst sert si, pour chaque jeu de données comparé et chaque page, ses documents diffèrent de ceux de LibreOffice de moins de ce seuil.", Kind: Number, Default: "1", Min: 0, Max: 50, Active: true},
	{Key: "native_workers", Group: "native", Label: "Processus Typst", Help: "Un processus produit plusieurs centaines de documents par seconde : 1 suffit le plus souvent.", Kind: Number, Default: "1", Min: 1, Max: 8, Active: true, Restart: true},

	{Key: "async_workers", Group: "async", Label: "Documents produits en parallèle", Help: "Demandes de POST /v1/render/async traitées en même temps ; elles partagent les workers LibreOffice avec les autres demandes.", Kind: Number, Default: "2", Min: 1, Max: 16, Active: true, Restart: true},
	{Key: "async_queue_size", Group: "async", Label: "Demandes en attente", Help: "Au-delà, DocIbis répond « occupé » (503). Les demandes attendent en mémoire : un redémarrage les interrompt.", Kind: Number, Default: "200", Min: 1, Max: 10000, Active: true},
	{Key: "async_retention_minutes", Group: "async", Label: "Conservation des documents (minutes)", Help: "Un document produit en arrière-plan se lit par GET /v1/jobs/{id}/document pendant ce délai, puis il est effacé.", Kind: Number, Default: "60", Min: 1, Max: 1440, Active: true},
	{Key: "async_url_allowlist", Group: "async", Label: "Domaines autorisés pour les rappels et les dépôts", Help: "Un domaine par ligne, sous-domaines compris (par exemple app.exemple.fr, mon-bucket.s3.eu-central-1.amazonaws.com). callbackUrl et uploadUrl ne visent que ces domaines, en HTTPS ; vide : ils sont refusés. Les rappels sont signés avec la variable d'environnement WEBHOOK_SECRET.", Kind: List, Max: 2000, Active: true},

	{Key: "blob_store", Group: "storage", Label: "Emplacement des fichiers", Help: "db (dans la base), s3://bucket/préfixe, r2://bucket/préfixe.", Kind: Text, Default: "db", Max: 300, Env: "BLOB_STORE", Restart: true},
	{Key: "blob_endpoint", Group: "storage", Label: "Point d'accès compatible S3", Help: "MinIO, Scaleway, OVHcloud… Vide pour AWS S3.", Kind: Text, Max: 300, Env: "BLOB_ENDPOINT", Restart: true},

	{Key: "history_retention_days", Group: "history", Label: "Conservation de l'historique (jours)", Help: "Les documents produits (date, modèle, auteur, durée) sont oubliés passé ce délai.", Kind: Number, Default: "90", Min: 1, Max: 3650, Active: true},
	{Key: "keep_render_data", Group: "history", Label: "Garder les données pour produire à nouveau", Help: "Les données JSON et le modèle de chaque document sont gardés, compressés, pendant la durée ci-dessous : la page Documents produits peut alors produire à nouveau le document. Ces données sont souvent personnelles (clients). Désactivé : seules les informations du rendu sont gardées. Le document lui-même n'est jamais gardé.", Kind: Toggle, Default: "true", Active: true},
	{Key: "render_data_days", Group: "history", Label: "Conservation des données des documents (jours)", Help: "Passé ce délai, les données JSON d'un document sont effacées ; ses informations restent dans l'historique. 180 jours (6 mois) au plus.", Kind: Number, Default: "90", Min: 1, Max: 180, Active: true},

	{Key: "session_hours", Group: "security", Label: "Durée d'une session (heures)", Kind: Number, Default: "12", Min: 1, Max: 168, Active: true},
	{Key: "session_idle_minutes", Group: "security", Label: "Déconnexion après inactivité (minutes)", Help: "Une session restée sans usage pendant ce délai est fermée. 0 : seule la durée de la session compte.", Kind: Number, Default: "120", Min: 0, Max: 1440, Active: true},
	{Key: "password_min_length", Group: "security", Label: "Longueur minimale des mots de passe", Kind: Number, Default: "12", Min: 8, Max: 64, Active: true},
	{Key: "login_max_attempts", Group: "security", Label: "Essais avant blocage temporaire", Help: "Après ces essais ratés, le compte attend avant un nouvel essai (30 s, puis le double).", Kind: Number, Default: "5", Min: 3, Max: 20, Active: true},
	{Key: "mfa_required_for", Group: "security", Label: "Double authentification obligatoire pour", Help: "admin : les administrateurs ; all : tous les comptes ; none : personne (chacun peut l'activer dans son profil). Un compte concerné l'active à sa prochaine connexion, avec une application comme Microsoft Authenticator.", Kind: Choice, Default: "all", Options: []string{"none", "admin", "all"}, Env: "MFA_REQUIRED_FOR", Active: true},
	{Key: "api_key_expiry_days", Group: "security", Label: "Durée de vie des clés API (jours)", Help: "Durée proposée à la création d'une clé ; elle peut être choisie clé par clé.", Kind: Number, Default: "365", Min: 1, Max: 1825, Active: true},
	{Key: "api_rate_per_minute", Group: "security", Label: "Appels par minute et par clé", Help: "Au-delà, DocIbis répond 429 avec un délai pour réessayer. Une clé peut avoir sa propre limite.", Kind: Number, Default: "600", Min: 1, Max: 100000, Active: true},

	{Key: "log_level", Group: "logs", Label: "Niveau de détail", Help: "S'applique tout de suite. Les journaux ne contiennent jamais les données des documents.", Kind: Choice, Default: "info", Options: []string{"debug", "info", "warn", "error"}, Env: "LOG_LEVEL", Active: true},

	{Key: "smtp_host", Group: "mail", Label: "Serveur SMTP", Help: "Pour envoyer les alertes du monitoring par e-mail. Vide : pas d'e-mail. Le mot de passe se donne par la variable SMTP_PASSWORD.", Kind: Text, Max: 200, Env: "SMTP_HOST", Active: true},
	{Key: "smtp_port", Group: "mail", Label: "Port SMTP", Help: "587 (STARTTLS) le plus souvent ; 465 pour TLS direct.", Kind: Number, Default: "587", Min: 1, Max: 65535, Env: "SMTP_PORT", Active: true},
	{Key: "smtp_username", Group: "mail", Label: "Utilisateur SMTP", Help: "Vide si le serveur n'en demande pas.", Kind: Text, Max: 200, Env: "SMTP_USERNAME", Active: true},
	{Key: "smtp_from", Group: "mail", Label: "Expéditeur", Help: "Adresse qui envoie les alertes, par exemple docibis@exemple.fr.", Kind: Text, Max: 200, Env: "SMTP_FROM", Active: true},
}

// Lookup returns the definition of a key.
func Lookup(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// Check validates a value for a setting; "" means "back to the default".
func (d Def) Check(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	switch d.Kind {
	case Number:
		n, err := strconv.Atoi(v)
		if err != nil || n < d.Min || n > d.Max {
			return "", fmt.Errorf("%s : nombre entre %d et %d attendu", d.Label, d.Min, d.Max)
		}
		return strconv.Itoa(n), nil
	case Toggle:
		if v != "true" && v != "false" {
			return "", fmt.Errorf("%s : true ou false attendu", d.Label)
		}
	case Choice:
		if !slices.Contains(d.Options, v) {
			return "", fmt.Errorf("%s : valeur parmi %s attendue", d.Label, strings.Join(d.Options, ", "))
		}
	case List:
		var lines []string
		for _, l := range strings.Split(v, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
		v = strings.Join(lines, "\n")
	}
	if d.Max > 0 && d.Kind != Number && len(v) > d.Max {
		return "", fmt.Errorf("%s : %d caractères au plus", d.Label, d.Max)
	}
	if d.Key == "public_url" && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
		return "", fmt.Errorf("%s : adresse en https:// attendue", d.Label)
	}
	return v, nil
}

// Source says where the effective value comes from.
type Source string

const (
	FromDefault     Source = "default"
	FromConsole     Source = "console"
	FromEnvironment Source = "environment"
)

// Value is the effective value of a setting.
type Value struct {
	Def
	Value  string
	Source Source
}

// Service holds the saved values; reads are safe from any goroutine.
type Service struct {
	getenv func(string) string
	mu     sync.RWMutex
	saved  map[string]string
	// OnChange is called after a save with the keys that changed.
	OnChange func(keys []string)
}

// New loads the saved values.
func New(saved map[string]string, getenv func(string) string) *Service {
	if saved == nil {
		saved = map[string]string{}
	}
	return &Service{getenv: getenv, saved: saved}
}

// Get returns the effective value of a setting.
func (s *Service) Get(key string) Value {
	d, ok := Lookup(key)
	if !ok {
		panic("unknown setting " + key)
	}
	if d.Env != "" {
		if v := s.getenv(d.Env); v != "" {
			return Value{d, v, FromEnvironment}
		}
	}
	s.mu.RLock()
	v, ok := s.saved[key]
	s.mu.RUnlock()
	if ok {
		return Value{d, v, FromConsole}
	}
	return Value{d, d.Default, FromDefault}
}

// String returns the effective value.
func (s *Service) String(key string) string { return s.Get(key).Value }

// Int returns the effective value of a number setting.
func (s *Service) Int(key string) int {
	n, err := strconv.Atoi(s.Get(key).Value)
	if err != nil {
		d, _ := Lookup(key)
		n, _ = strconv.Atoi(d.Default)
	}
	return n
}

// All returns every setting with its effective value.
func (s *Service) All() []Value {
	out := make([]Value, 0, len(Defs))
	for _, d := range Defs {
		out = append(out, s.Get(d.Key))
	}
	return out
}

// Saver writes values to the database.
type Saver interface {
	SaveSettings(ctx context.Context, values map[string]string) error
}

// InvalidError is a value refused by the checks, shown to the administrator.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &InvalidError{fmt.Sprintf(format, args...)}
}

// Update checks then saves several values. A setting fixed by an
// environment variable cannot be changed.
func (s *Service) Update(ctx context.Context, db Saver, values map[string]string) error {
	clean := make(map[string]string, len(values))
	for k, v := range values {
		d, ok := Lookup(k)
		if !ok {
			return invalid("réglage inconnu : %s", k)
		}
		if d.Env != "" && s.getenv(d.Env) != "" {
			return invalid("%s est fixé par la variable %s", d.Label, d.Env)
		}
		c, err := d.Check(v)
		if err != nil {
			return &InvalidError{err.Error()}
		}
		clean[k] = c
	}
	if err := db.SaveSettings(ctx, clean); err != nil {
		return err
	}
	s.mu.Lock()
	keys := make([]string, 0, len(clean))
	for k, v := range clean {
		if v == "" {
			delete(s.saved, k)
		} else {
			s.saved[k] = v
		}
		keys = append(keys, k)
	}
	s.mu.Unlock()
	if s.OnChange != nil {
		s.OnChange(keys)
	}
	return nil
}
