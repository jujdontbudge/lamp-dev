fn main() {
    let out = std::env::args().nth(1).expect("output directory argument");
    prost_build::Config::new()
        .out_dir(out)
        .compile_protos(&["contracts/worker/worker.proto"], &["contracts/worker"])
        .expect("generate worker protocol");
}
