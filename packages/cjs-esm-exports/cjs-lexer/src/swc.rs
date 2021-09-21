use crate::cjs_lexer::CJSLexer;
use crate::error::{DiagnosticBuffer, ErrorBuffer};

use std::{path::Path, rc::Rc};
use swc_common::{
	comments::SingleThreadedComments,
	errors::{Handler, HandlerFlags},
	FileName, SourceMap,
};
use swc_ecmascript::{
	ast::{Module, Program},
	parser::{lexer::Lexer, EsConfig, JscTarget, StringInput, Syntax},
	visit::FoldWith,
};

#[derive(Clone)]
pub struct SWC {
	pub specifier: String,
	pub module: Module,
	pub source_map: Rc<SourceMap>,
	pub comments: SingleThreadedComments,
}

impl SWC {
	/// parse source code.
	pub fn parse(specifier: &str, source: &str) -> Result<Self, anyhow::Error> {
		let source_map = SourceMap::default();
		let source_file = source_map.new_source_file(
			FileName::Real(Path::new(specifier).to_path_buf()),
			source.into(),
		);
		let sm = &source_map;
		let error_buffer = ErrorBuffer::new(specifier);
		let syntax = Syntax::Es(get_es_config(false));
		let input = StringInput::from(&*source_file);
		let comments = SingleThreadedComments::default();
		let lexer = Lexer::new(syntax, JscTarget::Es2020, input, Some(&comments));
		let mut parser = swc_ecmascript::parser::Parser::new_from(lexer);
		let handler = Handler::with_emitter_and_flags(
			Box::new(error_buffer.clone()),
			HandlerFlags {
				can_emit_warnings: true,
				dont_buffer_diagnostics: true,
				..HandlerFlags::default()
			},
		);
		let module = parser
			.parse_module()
			.map_err(move |err| {
				let mut diagnostic = err.into_diagnostic(&handler);
				diagnostic.emit();
				DiagnosticBuffer::from_error_buffer(error_buffer, |span| sm.lookup_char_pos(span.lo))
			})
			.unwrap();

		Ok(SWC {
			specifier: specifier.into(),
			module,
			source_map: Rc::new(source_map),
			comments,
		})
	}

	/// parse export names in the module.
	pub fn parse_cjs_exports(&self) -> Result<(Vec<String>, Vec<String>), anyhow::Error> {
		let program = Program::Module(self.module.clone());
		let mut parser = CJSLexer {
			exports: vec![],
			reexports: vec![],
		};
		program.fold_with(&mut parser);
		Ok((parser.exports, parser.reexports))
	}
}

fn get_es_config(jsx: bool) -> EsConfig {
	EsConfig {
		class_private_methods: true,
		class_private_props: true,
		class_props: true,
		dynamic_import: true,
		export_default_from: true,
		export_namespace_from: true,
		num_sep: true,
		nullish_coalescing: true,
		optional_chaining: true,
		top_level_await: true,
		import_meta: true,
		import_assertions: true,
		jsx,
		..EsConfig::default()
	}
}
