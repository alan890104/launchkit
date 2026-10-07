use axum::{routing::get, Json, Router};
use serde::Serialize;
use std::env;

#[derive(Serialize)]
struct Response {
    status: &'static str,
    message: &'static str,
}

async fn root() -> Json<Response> {
    Json(Response {
        status: "ok",
        message: "Hello from LaunchKit (Rust)!",
    })
}

async fn health() -> Json<Response> {
    Json(Response {
        status: "healthy",
        message: "ok",
    })
}

#[tokio::main]
async fn main() {
    let port = env::var("PORT").unwrap_or_else(|_| "8080".to_string());
    let addr = format!("0.0.0.0:{port}");

    let app = Router::new()
        .route("/", get(root))
        .route("/health", get(health));

    let listener = tokio::net::TcpListener::bind(&addr).await.unwrap();
    println!("Listening on {addr}");
    axum::serve(listener, app).await.unwrap();
}
